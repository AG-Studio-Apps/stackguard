package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// maxResponseBytes caps a single Engine API response the agent decodes, so a
// buggy or hostile daemon (or a genuinely enormous fleet) cannot grow the
// agent's memory without bound. Generous: a /containers/json for thousands of
// containers is a few MB. The event stream is deliberately long-lived and is
// not capped as a whole; its individual objects are tiny.
const maxResponseBytes = 32 << 20 // 32 MiB

// decodeBounded decodes JSON from a response body, reading at most
// maxResponseBytes. Beyond the cap the decode fails rather than the process.
func decodeBounded(body io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(body, maxResponseBytes)).Decode(v)
}

// isContainerID reports whether s is a Docker/Podman container id (hex, 12–64
// chars). The agent only ever inspects ids it read from the daemon, but a
// hostile daemon could return an "id" carrying `/` or `?` to retarget the API
// path — refuse anything that is not a plain id.
func isContainerID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// dockerClient talks the Engine API over the unix socket — stackGuard is on
// the host, so it takes the short way (the app reaches a daemon over SSH or
// Portainer because it is never on the host). Raw HTTP, no Docker SDK, to keep
// the binary tiny. Unversioned paths let the daemon pick its default API.
type dockerClient struct {
	http *http.Client
}

func newDockerClient(socket string) *dockerClient {
	return &dockerClient{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}}
}

func (d *dockerClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	return d.http.Do(req)
}

type versionInfo struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
	Os         string `json:"Os"`
	Arch       string `json:"Arch"`
}

func (d *dockerClient) version(ctx context.Context) (versionInfo, error) {
	var v versionInfo
	resp, err := d.get(ctx, "/version")
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("version: status %d", resp.StatusCode)
	}
	return v, decodeBounded(resp.Body, &v)
}

type containerSummary struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	State  string   `json:"State"`  // "running", "exited", "restarting", ...
	Status string   `json:"Status"` // "Up 3 weeks (healthy)", "Exited (137) 2h ago"
}

// list returns all containers (all=1), summaries only.
func (d *dockerClient) list(ctx context.Context) ([]containerSummary, error) {
	resp, err := d.get(ctx, "/containers/json?all=1")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list: status %d", resp.StatusCode)
	}
	var out []containerSummary
	return out, decodeBounded(resp.Body, &out)
}

// inspect returns the full state. errNotFound signals the container is gone.
func (d *dockerClient) inspect(ctx context.Context, id string) (containerInspect, error) {
	var c containerInspect
	if !isContainerID(id) {
		return c, fmt.Errorf("refusing to inspect malformed id %q", short(id))
	}
	resp, err := d.get(ctx, "/containers/"+id+"/json")
	if err != nil {
		return c, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return c, decodeBounded(resp.Body, &c)
	case http.StatusNotFound:
		return c, errNotFound
	default:
		return c, fmt.Errorf("inspect %s: status %d", id, resp.StatusCode)
	}
}

var errNotFound = fmt.Errorf("not found")

// dockerEvent is one line of GET /events.
type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// streamEvents yields container events until the context ends or the stream
// breaks (daemon restart). The caller reconnects; the sweep covers the gap.
func (d *dockerClient) streamEvents(ctx context.Context, onEvent func(dockerEvent)) error {
	resp, err := d.get(ctx, `/events?filters={"type":["container"]}`)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("events: status %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var ev dockerEvent
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
		onEvent(ev)
	}
}

// baseAction strips Docker's payload suffix ("exec_create: sh -c ...").
func (e dockerEvent) baseAction() string {
	for i := 0; i < len(e.Action); i++ {
		if e.Action[i] == ':' {
			return e.Action[:i]
		}
	}
	return e.Action
}

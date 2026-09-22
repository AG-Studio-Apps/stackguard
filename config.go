package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// config is everything stackGuard is told, all of it from the environment so
// no secret — and no relay address — is ever baked into an image layer. The
// three secrets — the host id, the host token and the 32-byte payload key —
// are minted by the app at enrolment; the app is the only other holder, and
// it names the relay too. The env var names are the ones
// the meshDeck app already writes in AgentDeployment, so this binary is a
// drop-in for the earlier Swift build.
type config struct {
	hostName  string
	hostID    string
	hostToken string
	key       []byte
	relayHost string
	relayPort int
	sockets   []engineSocket
	sweep     time.Duration
	heartbeat time.Duration
}

// engineSocket is one Engine API socket and the engine behind it. One agent
// watches every socket of its privilege domain — root's Docker and root's
// Podman together, a user's rootless Podman on its own — and each alert
// names the engine so the app can land on the right environment.
type engineSocket struct {
	engine string // "docker" or "podman"
	path   string
}

var knownEngines = map[string]bool{"docker": true, "podman": true}

const (
	defaultRelayPort = 443
	defaultSocket    = "/var/run/docker.sock"
	defaultSweep     = 60 * time.Second
	defaultHeartbeat = 120 * time.Second
)

// loadConfig reads the environment. Anything wrong is named plainly: the only
// person who will ever read the message is looking at `docker logs`.
func loadConfig(getenv func(string) string) (config, error) {
	var c config
	required := func(name string) (string, error) {
		v := getenv(name)
		if v == "" {
			return "", fmt.Errorf("%s is not set", name)
		}
		return v, nil
	}
	seconds := func(name string, fallback time.Duration) (time.Duration, error) {
		raw := getenv(name)
		if raw == "" {
			return fallback, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s must be a whole number of seconds", name)
		}
		return time.Duration(n) * time.Second, nil
	}

	var err error
	if c.hostName, err = required("MESHDECK_HOST_NAME"); err != nil {
		return c, err
	}
	if c.hostID, err = required("MESHDECK_HOST_ID"); err != nil {
		return c, err
	}
	if c.hostToken, err = required("MESHDECK_HOST_TOKEN"); err != nil {
		return c, err
	}
	encoded, err := required("MESHDECK_KEY")
	if err != nil {
		return c, err
	}
	c.key, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(c.key) != 32 {
		return c, fmt.Errorf("MESHDECK_KEY must be the base64 of a 32-byte key, as the app printed it")
	}

	// The relay is the app's to name: nothing here knows one, so the binary
	// can be read, built and run by anyone against whatever relay they run.
	if c.relayHost, err = required("MESHDECK_RELAY_HOST"); err != nil {
		return c, err
	}
	c.relayPort = defaultRelayPort
	if raw := getenv("MESHDECK_RELAY_PORT"); raw != "" {
		if p, perr := strconv.Atoi(raw); perr == nil && p > 0 {
			c.relayPort = p
		} else {
			return c, fmt.Errorf("MESHDECK_RELAY_PORT must be a port number")
		}
	}
	if c.sockets, err = parseSockets(getenv("MESHDECK_SOCKETS"), getenv("MESHDECK_DOCKER_SOCKET")); err != nil {
		return c, err
	}
	if c.sweep, err = seconds("MESHDECK_SWEEP_SECONDS", defaultSweep); err != nil {
		return c, err
	}
	if c.heartbeat, err = seconds("MESHDECK_HEARTBEAT_SECONDS", defaultHeartbeat); err != nil {
		return c, err
	}
	return c, nil
}

// parseSockets reads MESHDECK_SOCKETS — `engine=path` entries separated by
// commas, e.g. `docker=/var/run/docker.sock,podman=/run/podman/podman.sock`.
// Unset, it is the one Docker socket MESHDECK_DOCKER_SOCKET names, or the
// default, so an older deployment's environment still means what it meant.
func parseSockets(list, single string) ([]engineSocket, error) {
	if strings.TrimSpace(list) == "" {
		if single == "" {
			single = defaultSocket
		}
		return []engineSocket{{engine: "docker", path: single}}, nil
	}
	var out []engineSocket
	seen := map[string]bool{}
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		engine, path, ok := strings.Cut(entry, "=")
		engine, path = strings.TrimSpace(engine), strings.TrimSpace(path)
		if !ok || !knownEngines[engine] || path == "" {
			return nil, fmt.Errorf("MESHDECK_SOCKETS entry %q must be docker=<path> or podman=<path>", entry)
		}
		if seen[path] {
			return nil, fmt.Errorf("MESHDECK_SOCKETS names %s twice", path)
		}
		seen[path] = true
		out = append(out, engineSocket{engine: engine, path: path})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("MESHDECK_SOCKETS names no socket")
	}
	return out, nil
}

// redactedSummary is what may be logged — never the token, never the key. A
// host's log is read over a shoulder more often than a keychain is.
func (c config) redactedSummary() string {
	sockets := make([]string, 0, len(c.sockets))
	for _, s := range c.sockets {
		sockets = append(sockets, s.engine+"="+s.path)
	}
	return fmt.Sprintf("host %s · relay %s:%d · sockets %s · sweep %s · heartbeat %s",
		c.hostID, c.relayHost, c.relayPort, strings.Join(sockets, ","), c.sweep, c.heartbeat)
}

// envFunc is os.Getenv wrapped so loadConfig stays testable.
func envFunc() func(string) string { return os.Getenv }

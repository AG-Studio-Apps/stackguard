package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// relayClient posts sealed events and heartbeats to the relay over HTTPS,
// verified against the system trust store (the agent talks to exactly one
// public host; there is nobody on a headless box to answer a trust prompt).
type relayClient struct {
	base      string // https://host:port
	hostID    string
	hostToken string
	http      *http.Client
	log       *slog.Logger
}

func newRelayClient(host string, port int, hostID, hostToken string, log *slog.Logger) *relayClient {
	base := fmt.Sprintf("https://%s", host)
	if port != 443 {
		base = fmt.Sprintf("https://%s:%d", host, port)
	}
	return &relayClient{
		base: base, hostID: hostID, hostToken: hostToken,
		http: &http.Client{Timeout: 20 * time.Second},
		log:  log,
	}
}

// postEvent is fire-and-forget, as the protocol requires: 204 means accepted,
// cooled down or muted — none is a reason to retry. 403 means the enrolment is
// gone (re-enrol from the app); it is logged, not retried.
func (r *relayClient) postEvent(ctx context.Context, kind, collapse, blob string) {
	body, _ := json.Marshal(map[string]string{"kind": kind, "collapse": collapse, "blob": blob})
	status := r.post(ctx, "/v1/hosts/"+r.hostID+"/events", body)
	switch {
	case status == http.StatusNoContent:
		r.log.Info("sent", "kind", kind)
	case status == http.StatusForbidden || status == http.StatusNotFound:
		r.log.Error("relay does not know this host — alerts are not enrolled; re-enrol from the app")
	default:
		r.log.Warn("could not send alert", "kind", kind, "status", status)
	}
}

// heartbeat, every two minutes — what makes the relay's "host silent" alert
// mean anything.
func (r *relayClient) heartbeat(ctx context.Context) {
	status := r.post(ctx, "/v1/hosts/"+r.hostID+"/heartbeat", []byte("{}"))
	if status != http.StatusNoContent && status != 0 {
		if status == http.StatusForbidden {
			r.log.Error("relay does not know this host — re-enrol from the app")
		} else {
			r.log.Warn("heartbeat failed", "status", status)
		}
	}
}

// post returns the HTTP status, or 0 on a transport failure (logged).
func (r *relayClient) post(ctx context.Context, path string, body []byte) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+path, bytes.NewReader(body))
	if err != nil {
		r.log.Warn("build request", "err", err)
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.hostToken)
	resp, err := r.http.Do(req)
	if err != nil {
		r.log.Warn("relay unreachable", "err", err)
		return 0
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

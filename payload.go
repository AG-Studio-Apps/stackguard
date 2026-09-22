package main

import "time"

// AlertPayload is the plaintext inside the encrypted blob — the contract with
// the app's MeshDeckAlertPayload (Packages/MeshDeckCore/.../AlertPayload.swift).
// The relay forwards the blob unread; the Notification Service Extension
// decodes this on the device. Field names and types must match the Swift
// Codable exactly: `v` (=1), and an ISO8601/RFC3339 `at`. `engine` names
// which of the host's engines the container lives in ("docker"/"podman"); it
// is optional on both ends — omitted here when unknown, ignored by an app
// that predates it — so the version did not move.
type AlertPayload struct {
	Version   int    `json:"v"`
	Host      string `json:"host"`
	Engine    string `json:"engine,omitempty"`
	Container string `json:"container"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail"`
	At        string `json:"at"`
}

const payloadVersion = 1

// newPayload builds a payload with the timestamp formatted the way Swift's
// JSONEncoder.dateEncodingStrategy = .iso8601 expects to decode it: RFC3339 in
// UTC, no fractional seconds (ISO8601DateFormatter's default).
func newPayload(host, engine, container, kind, detail string, at time.Time) AlertPayload {
	return AlertPayload{
		Version:   payloadVersion,
		Host:      host,
		Engine:    engine,
		Container: container,
		Kind:      kind,
		Detail:    detail,
		At:        at.UTC().Format(time.RFC3339),
	}
}

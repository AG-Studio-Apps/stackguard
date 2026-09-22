package main

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Alert kinds — the wire names the relay and app understand (AlertKind).
const (
	kindDown      = "down"
	kindUnhealthy = "unhealthy"
	kindDisk      = "disk"
	kindRecovered = "recovered"
)

// agentAlert is one thing worth telling the phone about. The collapse key is
// opaque and stable per subject: the relay hashes it and the protocol forbids
// anything that identifies the host's contents, so it is always a digest.
type agentAlert struct {
	kind     string
	collapse string
	subject  string // what shows after "<host> · ": a container name, or "disk"
	detail   string
	at       time.Time
}

// collapseContainer mirrors CollapseKey.container — the first 16 bytes of
// sha256("container:"+id) as hex, so even a stolen relay store cannot be
// joined against a list of container ids.
func collapseContainer(id string) string { return digest("container:" + id) }

func collapseDisk(hostID string) string { return digest("disk:" + hostID) }

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

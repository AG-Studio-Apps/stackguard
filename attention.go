package main

import (
	"fmt"
	"strings"
	"time"
)

// attention is the container health verdict, mirroring the app's
// ContainerAttention (Packages/.../ContainerStatus.swift) so a stackGuard
// alert and a meshDeck card never disagree about what "down" means.
type attention int

const (
	attnNominal      attention = iota // running, healthy or no healthcheck
	attnTransitional                  // starting / restarting-clean / removing
	attnDegraded                      // running but unhealthy
	attnDown                          // dead, exited non-zero, OOM-killed, restart loop
	attnIdle                          // created, paused, exited cleanly
)

func (a attention) needsAttention() bool { return a == attnDegraded || a == attnDown }

// restartLoopThreshold and recentExitWindow mirror AttentionDetail.
const (
	restartLoopThreshold = 3
	recentExitWindow     = 10 * time.Minute
)

// inspectState is the subset of `GET /containers/{id}/json` .State we need.
type inspectState struct {
	Status     string  `json:"Status"`
	Running    bool    `json:"Running"`
	Restarting bool    `json:"Restarting"`
	Paused     bool    `json:"Paused"`
	Dead       bool    `json:"Dead"`
	OOMKilled  bool    `json:"OOMKilled"`
	ExitCode   int     `json:"ExitCode"`
	FinishedAt string  `json:"FinishedAt"`
	Health     *health `json:"Health"`
}

type health struct {
	Status        string        `json:"Status"`
	FailingStreak int           `json:"FailingStreak"`
	Log           []healthProbe `json:"Log"`
}

type healthProbe struct {
	ExitCode int    `json:"ExitCode"`
	Output   string `json:"Output"`
}

type containerInspect struct {
	ID           string       `json:"Id"`
	Name         string       `json:"Name"`
	RestartCount int          `json:"RestartCount"`
	State        inspectState `json:"State"`
}

func (c containerInspect) displayName() string {
	return strings.TrimPrefix(c.Name, "/")
}

// attentionOf mirrors ContainerAttention(status:health:exitCode:oomKilled:).
func (c containerInspect) attention() attention {
	s := c.State
	if s.OOMKilled {
		return attnDown
	}
	healthStatus := ""
	if s.Health != nil {
		healthStatus = s.Health.Status
	}
	switch s.Status {
	case "running":
		switch healthStatus {
		case "unhealthy":
			return attnDegraded
		case "starting":
			return attnTransitional
		default: // healthy or none
			return attnNominal
		}
	case "restarting":
		if s.ExitCode == 0 {
			return attnTransitional
		}
		return attnDown
	case "removing":
		return attnTransitional
	case "dead":
		return attnDown
	case "exited":
		if s.ExitCode == 0 {
			return attnIdle
		}
		return attnDown
	default: // created, paused
		return attnIdle
	}
}

func (c containerInspect) finishedAt() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, c.State.FinishedAt)
	if err != nil || t.IsZero() || t.Year() <= 1 {
		return time.Time{}, false
	}
	return t, true
}

func (c containerInspect) isRestartLoop(now time.Time) bool {
	if c.RestartCount < restartLoopThreshold {
		return false
	}
	if c.State.Restarting {
		return true
	}
	if fin, ok := c.finishedAt(); ok {
		return now.Sub(fin) < recentExitWindow
	}
	return false
}

func (c containerInspect) diedRecently(now time.Time) bool {
	fin, ok := c.finishedAt()
	if !ok {
		return false
	}
	age := now.Sub(fin)
	return age >= 0 && age < recentExitWindow
}

// sentence is the one human line the banner shows — the same phrasing the
// app's cards use (AttentionDetail.reason + AgentWatcher.sentence).
func (c containerInspect) sentence(now time.Time) string {
	a := c.attention()
	var reason string
	switch a {
	case attnNominal, attnIdle:
		return "Needs attention."
	case attnTransitional:
		if c.State.Restarting {
			reason = "restarting"
		} else {
			reason = "starting"
		}
	case attnDegraded:
		streak := 0
		var probe string
		if c.State.Health != nil {
			streak = c.State.Health.FailingStreak
			if n := len(c.State.Health.Log); n > 0 {
				probe = strings.TrimSpace(c.State.Health.Log[n-1].Output)
			}
		}
		if streak > 0 {
			plural := "s"
			if streak == 1 {
				plural = ""
			}
			reason = fmt.Sprintf("unhealthy · %d failed check%s", streak, plural)
		} else {
			reason = "unhealthy"
		}
		text := capitalise(reason)
		if probe != "" {
			if len(probe) > 120 {
				probe = probe[:120]
			}
			text += " · " + probe
		}
		return text + "."
	case attnDown:
		switch {
		case c.State.OOMKilled:
			reason = "killed · out of memory"
		case c.isRestartLoop(now):
			reason = fmt.Sprintf("restart loop · exit %d · restarted %d×", c.State.ExitCode, c.RestartCount)
		case c.State.Dead:
			reason = "dead"
		default:
			reason = fmt.Sprintf("exit %d", c.State.ExitCode)
		}
	}
	return capitalise(reason) + "."
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- summary path (the list call, no inspect) ---
//
// A sweep computes each container's verdict from the cheap list call and only
// inspects the ones that need attention, exactly as the app's ContainerListStore
// does. The list carries no structured Health object, so — unlike the inspect
// path — health is read from the Status suffix ("Up 2 hours (unhealthy)"). The
// live event stream's `health_status` action is the primary route for an
// unhealthy transition; this is the backstop.

// summaryAttention mirrors ContainerSummary.attention: ContainerAttention built
// from the list State, the health parsed from Status, and the exit code parsed
// from Status; oomKilled is unknowable from the list (false here, refined by a
// later inspect).
func (s containerSummary) summaryAttention() attention {
	switch s.State {
	case "running":
		switch s.statusHealth() {
		case "unhealthy":
			return attnDegraded
		case "starting":
			return attnTransitional
		default:
			return attnNominal
		}
	case "restarting":
		if s.exitCodeFromStatus() == 0 {
			return attnTransitional
		}
		return attnDown
	case "removing":
		return attnTransitional
	case "dead":
		return attnDown
	case "exited":
		if s.exitCodeFromStatus() == 0 {
			return attnIdle
		}
		return attnDown
	default: // created, paused
		return attnIdle
	}
}

// statusHealth reads the healthcheck suffix Docker appends to a running
// container's Status: "healthy", "unhealthy", "starting", or "" for none.
func (s containerSummary) statusHealth() string {
	switch {
	case strings.HasSuffix(s.Status, "(unhealthy)"):
		return "unhealthy"
	case strings.HasSuffix(s.Status, "(healthy)"):
		return "healthy"
	case strings.HasSuffix(s.Status, "(health: starting)"):
		return "starting"
	default:
		return ""
	}
}

// exitCodeFromStatus mirrors ContainerSummary.exitCodeFromStatus: the number in
// "Exited (137) 2 hours ago" / "Restarting (1) 4 seconds ago". 0 when absent.
func (s containerSummary) exitCodeFromStatus() int {
	if !strings.HasPrefix(s.Status, "Exited") && !strings.HasPrefix(s.Status, "Restarting") {
		return 0
	}
	open := strings.IndexByte(s.Status, '(')
	closeIdx := strings.IndexByte(s.Status, ')')
	if open < 0 || closeIdx < 0 || open >= closeIdx {
		return 0
	}
	var code int
	if _, err := fmt.Sscanf(s.Status[open+1:closeIdx], "%d", &code); err != nil {
		return 0
	}
	return code
}

// name mirrors ContainerSummary.name: strip Docker's leading slash and prefer
// the canonical name (the one with no inner slash — a link alias like /web/db
// can sort first).
func (s containerSummary) name() string {
	stripped := make([]string, 0, len(s.Names))
	for _, n := range s.Names {
		stripped = append(stripped, strings.TrimPrefix(n, "/"))
	}
	for _, n := range stripped {
		if !strings.Contains(n, "/") {
			return n
		}
	}
	if len(stripped) > 0 {
		return stripped[0]
	}
	if len(s.ID) >= 12 {
		return s.ID[:12]
	}
	return s.ID
}

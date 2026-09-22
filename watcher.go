package main

import "time"

// watcher is the decision half of stackGuard: handed observations, it answers
// with the alerts worth sending. No clock, no network, no Docker — the rules
// that decide what "down" means are unit-tested, ported faithfully from the
// app's AgentWatcher so a banner never contradicts a card.
//
// The rules:
//   - down → "down"; degraded → "unhealthy"; either replaces the other.
//   - a return to actually-running → "recovered".
//   - stopped cleanly / paused / removed clears silently: a crash-looping
//     container someone stops has not recovered, and saying so is a lie.
//   - starting / restarting-clean says nothing.
//   - the FIRST sweep adopts, it does not alert — an alert is about a change,
//     and the app already shows what is broken. The one exception is a
//     container that died in the last ten minutes, closing the gap between a
//     host booting and the agent starting.
type watcher struct {
	hostID          string
	outstanding     map[string]string // container id → the kind currently raised, or absent
	names           map[string]string
	diskOutstanding bool
	adopted         bool
}

const (
	diskAlertFraction = 0.10 // free space below this raises disk
	diskClearFraction = 0.15 // and only clears above this, so it does not flap
)

func newWatcher(hostID string) *watcher {
	return &watcher{hostID: hostID, outstanding: map[string]string{}, names: map[string]string{}}
}

// observation is one container as the Engine describes it now. inspect is nil
// until there is something to say — a healthy fleet costs one list call.
type observation struct {
	id      string
	name    string
	attn    attention
	inspect *containerInspect
}

// sweep takes a full list; anything the watcher knew and this list omits was
// removed, and is dropped without an alert.
func (w *watcher) sweep(obs []observation, now time.Time) []agentAlert {
	seen := map[string]bool{}
	for _, o := range obs {
		seen[o.id] = true
	}
	for id := range w.outstanding {
		if !seen[id] {
			delete(w.outstanding, id)
		}
	}
	var alerts []agentAlert
	for _, o := range obs {
		alerts = append(alerts, w.observe(o, now)...)
	}
	w.adopted = true
	return alerts
}

func outstandingKind(a attention) string {
	switch a {
	case attnDown:
		return kindDown
	case attnDegraded:
		return kindUnhealthy
	default: // nominal, idle, transitional — not news
		return ""
	}
}

// observe handles one container changing (an event arrived and it was
// re-inspected, or one entry of a sweep).
func (w *watcher) observe(o observation, now time.Time) []agentAlert {
	prev, had := w.outstanding[o.id]
	wanted := outstandingKind(o.attn)
	if wanted == "" {
		delete(w.outstanding, o.id)
	} else {
		w.outstanding[o.id] = wanted
	}
	w.names[o.id] = o.name

	prevKind := ""
	if had {
		prevKind = prev
	}

	// Adoption: take the state, stay quiet — unless it broke just now.
	if !w.adopted && !had {
		if wanted == kindDown && o.inspect != nil && o.inspect.diedRecently(now) {
			return []agentAlert{w.alert(wanted, o, now)}
		}
		return nil
	}
	if wanted == prevKind {
		return nil // no change
	}
	if wanted != "" {
		return []agentAlert{w.alert(wanted, o, now)}
	}
	// It is fine now. Only a return to actually-running is a recovery.
	if had && o.attn == attnNominal {
		return []agentAlert{{
			kind: kindRecovered, collapse: collapseContainer(o.id),
			subject: o.name, detail: "Running again.", at: now,
		}}
	}
	return nil
}

func (w *watcher) forget(id string) { delete(w.outstanding, id) }

// observeDisk is the free fraction of the filesystem the Docker data root sits
// on, with hysteresis so a filesystem hovering on the threshold does not flap.
func (w *watcher) observeDisk(freeFraction float64, detail string, now time.Time) []agentAlert {
	if !w.diskOutstanding && freeFraction < diskAlertFraction {
		w.diskOutstanding = true
		return []agentAlert{{
			kind: kindDisk, collapse: collapseDisk(w.hostID),
			subject: "disk", detail: detail, at: now,
		}}
	}
	if w.diskOutstanding && freeFraction >= diskClearFraction {
		w.diskOutstanding = false
	}
	return nil
}

func (w *watcher) alert(kind string, o observation, now time.Time) agentAlert {
	detail := "Needs attention."
	if o.inspect != nil {
		detail = o.inspect.sentence(now)
	}
	return agentAlert{kind: kind, collapse: collapseContainer(o.id), subject: o.name, detail: detail, at: now}
}

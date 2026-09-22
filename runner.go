package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// source is one engine socket and the decision state for what lives behind
// it. Containers of two engines on one host are disjoint sets with disjoint
// ids, so each source keeps its own watcher and never sees the other's list
// as "everything else was removed".
type source struct {
	engine string
	docker *dockerClient
	watch  *watcher
}

func newSource(engine string, docker *dockerClient, hostID string) *source {
	return &source{engine: engine, docker: docker, watch: newWatcher(hostID)}
}

// runner is the three loops, and nothing clever:
//
//  1. events — each engine's own stream, so a container that dies is noticed
//     the second it happens rather than at the next sweep; it reconnects
//     when the daemon restarts.
//  2. sweep — a full list of every engine every minute, catching whatever the
//     streams missed (a reconnect gap, an event the engine never emitted),
//     and carrying the one disk check.
//  3. heartbeat — every two minutes, which is what makes the relay's "host
//     silent" alert mean anything.
//
// The watchers are the only mutable decision state; a single mutex serialises
// the loops that touch them so they cannot race.
type runner struct {
	sources []*source
	relay   *relayClient
	cfg     config
	log     *slog.Logger
	now     func() time.Time

	mu   sync.Mutex
	disk *watcher // the filesystem is the host's, not an engine's: one check, one hysteresis
}

// interestingActions are the container events worth a re-inspect.
// health_status covers both the healthy and unhealthy transitions.
var interestingActions = map[string]bool{
	"die": true, "start": true, "stop": true, "kill": true, "oom": true,
	"restart": true, "health_status": true, "pause": true, "unpause": true,
	"destroy": true, "create": true,
}

func newRunner(sources []*source, relay *relayClient, cfg config, log *slog.Logger) *runner {
	return &runner{
		sources: sources, relay: relay, cfg: cfg, log: log,
		now:  time.Now,
		disk: newWatcher(cfg.hostID),
	}
}

// run blocks until ctx is cancelled.
func (r *runner) run(ctx context.Context) {
	r.log.Info("stackGuard starting", "config", r.cfg.redactedSummary())
	var wg sync.WaitGroup
	wg.Add(2 + len(r.sources))
	go func() { defer wg.Done(); r.heartbeatLoop(ctx) }()
	go func() { defer wg.Done(); r.sweepLoop(ctx) }()
	for _, s := range r.sources {
		go func(s *source) { defer wg.Done(); r.eventLoop(ctx, s) }(s)
	}
	wg.Wait()
	r.log.Info("stackGuard stopped")
}

func (r *runner) heartbeatLoop(ctx context.Context) {
	for {
		r.relay.heartbeat(ctx)
		if !sleep(ctx, r.cfg.heartbeat) {
			return
		}
	}
}

func (r *runner) sweepLoop(ctx context.Context) {
	for {
		for _, s := range r.sources {
			r.sweep(ctx, s)
		}
		r.checkDisk(ctx)
		if !sleep(ctx, r.cfg.sweep) {
			return
		}
	}
}

// eventLoop reconnects from scratch on any break; the sweep covers the gap.
func (r *runner) eventLoop(ctx context.Context, s *source) {
	backoff := time.Second
	for {
		err := s.docker.streamEvents(ctx, func(ev dockerEvent) {
			backoff = time.Second
			r.handleEvent(ctx, s, ev)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			r.log.Warn("event stream ended", "engine", s.engine, "err", err)
		}
		if !sleep(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}
}

// requestTimeout bounds one list or inspect: a wedged socket must not stall
// the other engine's sweep, or the disk check, behind it. The event stream
// is the one call that is meant to last.
const requestTimeout = 30 * time.Second

func (r *runner) sweep(ctx context.Context, s *source) {
	listCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	summaries, err := s.docker.list(listCtx)
	cancel()
	if err != nil {
		r.log.Warn("could not list containers", "engine", s.engine, "err", err)
		return
	}
	obs := make([]observation, 0, len(summaries))
	for _, summary := range summaries {
		obs = append(obs, r.observationFor(ctx, s, summary))
	}
	r.mu.Lock()
	alerts := s.watch.sweep(obs, r.now())
	r.mu.Unlock()
	for _, a := range alerts {
		r.send(ctx, s.engine, a)
	}
}

// observationFor computes the cheap verdict from the list, and only inspects a
// container that needs attention — a hundred healthy containers must not cost a
// hundred inspects a minute.
func (r *runner) observationFor(ctx context.Context, s *source, summary containerSummary) observation {
	attn := summary.summaryAttention()
	o := observation{id: summary.ID, name: summary.name(), attn: attn}
	if !attn.needsAttention() {
		return o
	}
	if inspect, ok := r.inspect(ctx, s, summary.ID); ok {
		o.inspect = &inspect
		o.attn = inspect.attention() // refine with the exit code / OOM flag
	}
	return o
}

func (r *runner) inspect(ctx context.Context, s *source, id string) (containerInspect, bool) {
	inspectCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	c, err := s.docker.inspect(inspectCtx, id)
	cancel()
	if err != nil {
		if err != errNotFound {
			r.log.Debug("could not inspect", "engine", s.engine, "id", short(id), "err", err)
		}
		return containerInspect{}, false
	}
	return c, true
}

func (r *runner) handleEvent(ctx context.Context, s *source, ev dockerEvent) {
	if ev.Type != "container" || !interestingActions[ev.baseAction()] {
		return
	}
	id := ev.Actor.ID
	if ev.baseAction() == "destroy" {
		r.mu.Lock()
		s.watch.forget(id)
		r.mu.Unlock()
		return
	}
	inspect, ok := r.inspect(ctx, s, id)
	if !ok {
		return
	}
	name := ev.Actor.Attributes["name"]
	if name == "" {
		name = inspect.displayName()
	}
	o := observation{id: id, name: name, attn: inspect.attention(), inspect: &inspect}
	r.mu.Lock()
	alerts := s.watch.observe(o, r.now())
	r.mu.Unlock()
	for _, a := range alerts {
		r.send(ctx, s.engine, a)
	}
}

func (r *runner) checkDisk(ctx context.Context) {
	// statfs the agent's own root: a container's writable layer lives under the
	// daemon's data root, so "/" inside this container is the filesystem that
	// fills up (see disk.go). It is the host's disk, whichever engine runs us.
	reading, ok := checkDisk("/")
	if !ok {
		return
	}
	r.mu.Lock()
	alerts := r.disk.observeDisk(reading.fraction, reading.detail, r.now())
	r.mu.Unlock()
	for _, a := range alerts {
		r.send(ctx, "", a)
	}
}

// send seals the alert with the host's payload key and posts it. The relay
// never sees the host name, the engine, the container name or the reason —
// only the kind, an opaque collapse key and the ciphertext.
func (r *runner) send(ctx context.Context, engine string, a agentAlert) {
	payload := newPayload(r.cfg.hostName, engine, a.subject, a.kind, a.detail, a.at)
	blob, err := seal(payload, r.cfg.key, r.cfg.hostID)
	if err != nil {
		r.log.Error("could not seal alert", "kind", a.kind, "err", err)
		return
	}
	// The host's own log may name its containers; the relay never does.
	r.log.Debug("alert", "engine", engine, "kind", a.kind, "subject", a.subject, "detail", a.detail)
	r.relay.postEvent(ctx, a.kind, a.collapse, blob)
}

// sleep waits d, or returns false as soon as ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

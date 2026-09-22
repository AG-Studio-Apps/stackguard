// Command stackguard is the meshDeck alert agent. It reads the engine sockets
// it is given — Docker's, Podman's, or both — and writes sealed events to one
// relay URL, and it does nothing else: no shell, no actions, no inbound port.
// A container it watches is never touched — every action in meshDeck goes
// through the app's own connection to the host, not through here.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	log := newLogger(os.Getenv("MESHDECK_LOG_LEVEL"))

	cfg, err := loadConfig(envFunc())
	if err != nil {
		// Exit 78 is EX_CONFIG: a restart policy keeps trying, and the log
		// names the wrong variable rather than looping in silence.
		log.Error("configuration", "err", err)
		os.Exit(78)
	}

	// Every socket is pinged once so the log says what answered. One that
	// does not is named but kept: its event loop keeps reconnecting and its
	// sweep keeps trying, so a Podman socket that comes up after a reboot is
	// watched from the moment it does. None answering is the old exit 69 —
	// the restart policy keeps trying from scratch.
	var sources []*source
	reachable := 0
	for _, s := range cfg.sockets {
		docker := newDockerClient(s.path)
		pingCtx, cancelPing := context.WithTimeout(context.Background(), 10*time.Second)
		version, err := docker.version(pingCtx)
		cancelPing()
		if err != nil {
			log.Error("cannot reach the engine socket yet; will keep trying", "engine", s.engine, "socket", s.path, "err", err)
		} else {
			reachable++
			log.Info(s.engine, "socket", s.path, "version", version.Version, "api", version.APIVersion,
				"platform", version.Os+"/"+version.Arch)
		}
		sources = append(sources, newSource(s.engine, docker, cfg.hostID))
	}
	if reachable == 0 {
		os.Exit(69) // EX_UNAVAILABLE
	}

	relay := newRelayClient(cfg.relayHost, cfg.relayPort, cfg.hostID, cfg.hostToken, log)
	r := newRunner(sources, relay, cfg, log)

	// Docker sends SIGTERM and waits ten seconds; cancelling lets the loops
	// fall out of their sleeps and exit straight away rather than being killed.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	r.run(ctx)
}

// newLogger writes structured lines to stdout at the level MESHDECK_LOG_LEVEL
// names (debug/info/warn/error), defaulting to info.
func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

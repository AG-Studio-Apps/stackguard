package main

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1800000000, 0)

func running(name string) *containerInspect {
	return &containerInspect{ID: "c-" + name, Name: "/" + name,
		State: inspectState{Status: "running"}}
}
func exited(name string, code int, finished time.Time) *containerInspect {
	return &containerInspect{ID: "c-" + name, Name: "/" + name,
		State: inspectState{Status: "exited", ExitCode: code, FinishedAt: finishedStr(finished)}}
}
func unhealthy(name string, streak int, output string) *containerInspect {
	c := running(name)
	c.State.Health = &health{Status: "unhealthy", FailingStreak: streak}
	if output != "" {
		c.State.Health.Log = []healthProbe{{ExitCode: 1, Output: output}}
	}
	return c
}
func oom(name string, finished time.Time) *containerInspect {
	c := exited(name, 137, finished)
	c.State.OOMKilled = true
	return c
}
func restartLoop(name string) *containerInspect {
	return &containerInspect{ID: "c-" + name, Name: "/" + name, RestartCount: 6,
		State: inspectState{Status: "restarting", ExitCode: 1, Restarting: true,
			FinishedAt: finishedStr(testNow.Add(-30 * time.Second))}}
}
func finishedStr(t time.Time) string {
	if t.IsZero() {
		return "0001-01-01T00:00:00Z"
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func obs(c *containerInspect) observation {
	return observation{id: c.ID, name: c.displayName(), attn: c.attention(), inspect: c}
}

func TestAdoptionIsSilent(t *testing.T) {
	w := newWatcher("h1")
	old := exited("sonarr", 1, testNow.Add(-7*24*time.Hour))
	if a := w.sweep([]observation{obs(old), obs(running("radarr"))}, testNow); len(a) != 0 {
		t.Fatalf("adoption should be silent, got %d alerts", len(a))
	}
}

func TestAdoptionReportsRecentDeath(t *testing.T) {
	w := newWatcher("h1")
	a := w.sweep([]observation{obs(exited("sonarr", 137, testNow.Add(-60*time.Second)))}, testNow)
	if len(a) != 1 || a[0].kind != kindDown || a[0].detail != "Exit 137." {
		t.Fatalf("want one down 'Exit 137.', got %+v", a)
	}
}

func TestDownThenRecovered(t *testing.T) {
	w := newWatcher("h1")
	w.sweep([]observation{obs(running("sonarr"))}, testNow)
	down := w.observe(obs(exited("sonarr", 1, testNow)), testNow)
	if len(down) != 1 || down[0].kind != kindDown {
		t.Fatalf("want down, got %+v", down)
	}
	if a := w.observe(obs(exited("sonarr", 1, testNow)), testNow); len(a) != 0 {
		t.Fatalf("same trouble twice is not news, got %+v", a)
	}
	back := w.observe(obs(running("sonarr")), testNow)
	if len(back) != 1 || back[0].kind != kindRecovered || back[0].detail != "Running again." {
		t.Fatalf("want recovered, got %+v", back)
	}
	if back[0].collapse != down[0].collapse {
		t.Fatal("recovery must clear the same collapse key")
	}
}

func TestUnhealthyCarriesProbeThenDown(t *testing.T) {
	w := newWatcher("h1")
	w.sweep([]observation{obs(running("sonarr"))}, testNow)
	sick := w.observe(obs(unhealthy("sonarr", 3, "curl: (7) connection refused")), testNow)
	want := "Unhealthy · 3 failed checks · curl: (7) connection refused."
	if len(sick) != 1 || sick[0].kind != kindUnhealthy || sick[0].detail != want {
		t.Fatalf("want %q, got %+v", want, sick)
	}
	dead := w.observe(obs(exited("sonarr", 1, testNow)), testNow)
	if len(dead) != 1 || dead[0].kind != kindDown {
		t.Fatalf("degraded→down should replace, got %+v", dead)
	}
}

func TestReasons(t *testing.T) {
	w := newWatcher("h1")
	w.sweep(nil, testNow)
	loop := w.observe(obs(restartLoop("sonarr")), testNow)
	if len(loop) != 1 || !strings.HasPrefix(loop[0].detail, "Restart loop · exit 1 · restarted 6×") {
		t.Fatalf("want restart loop, got %+v", loop)
	}
	w2 := newWatcher("h1")
	w2.sweep(nil, testNow)
	o := w2.observe(obs(oom("sonarr", testNow)), testNow)
	if len(o) != 1 || o[0].detail != "Killed · out of memory." {
		t.Fatalf("want OOM, got %+v", o)
	}
}

func TestStoppingIsNotRecovery(t *testing.T) {
	w := newWatcher("h1")
	w.sweep([]observation{obs(running("sonarr"))}, testNow)
	w.observe(obs(restartLoop("sonarr")), testNow)
	if a := w.observe(obs(exited("sonarr", 0, testNow)), testNow); len(a) != 0 {
		t.Fatalf("clean stop is not a recovery, got %+v", a)
	}
	if a := w.observe(obs(running("sonarr")), testNow); len(a) != 0 {
		t.Fatalf("nothing outstanding, got %+v", a)
	}
}

func TestRemovalIsSilent(t *testing.T) {
	w := newWatcher("h1")
	w.sweep([]observation{obs(running("sonarr"))}, testNow)
	w.observe(obs(exited("sonarr", 1, testNow)), testNow)
	if a := w.sweep(nil, testNow); len(a) != 0 {
		t.Fatalf("removal is silent, got %+v", a)
	}
	if a := w.observe(obs(running("sonarr")), testNow); len(a) != 0 {
		t.Fatalf("came back after removal, nothing outstanding, got %+v", a)
	}
}

func TestDiskHysteresis(t *testing.T) {
	w := newWatcher("h1")
	if a := w.observeDisk(0.5, "half", testNow); len(a) != 0 {
		t.Fatal("plenty of disk, no alert")
	}
	a := w.observeDisk(0.04, "96% full", testNow)
	if len(a) != 1 || a[0].kind != kindDisk || a[0].subject != "disk" {
		t.Fatalf("want disk alert, got %+v", a)
	}
	if len(w.observeDisk(0.03, "97% full", testNow)) != 0 {
		t.Fatal("already outstanding, no repeat")
	}
	if len(w.observeDisk(0.12, "88% full", testNow)) != 0 {
		t.Fatal("under the clear mark stays quiet")
	}
	if len(w.observeDisk(0.40, "60% full", testNow)) != 0 {
		t.Fatal("clearing does not alert")
	}
	if len(w.observeDisk(0.04, "96% full", testNow)) != 1 {
		t.Fatal("re-armed after real recovery")
	}
}

func TestCollapseKeysOpaque(t *testing.T) {
	k := collapseContainer("c0ffee1234567890")
	if len(k) != 32 || strings.Contains(k, "c0ffee") {
		t.Fatalf("collapse must be an opaque 32-hex digest, got %q", k)
	}
	if k != collapseContainer("c0ffee1234567890") {
		t.Fatal("must be stable")
	}
	if collapseDisk("h1") == collapseDisk("h2") {
		t.Fatal("disk keys differ per host")
	}
}

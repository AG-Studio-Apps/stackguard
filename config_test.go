package main

import (
	"reflect"
	"testing"
)

func TestParseSockets(t *testing.T) {
	cases := []struct {
		list, single string
		want         []engineSocket
		bad          bool
	}{
		{"", "", []engineSocket{{"docker", "/var/run/docker.sock"}}, false},
		{"", "/run/docker.sock", []engineSocket{{"docker", "/run/docker.sock"}}, false},
		{"docker=/var/run/docker.sock,podman=/run/podman/podman.sock", "/ignored",
			[]engineSocket{{"docker", "/var/run/docker.sock"}, {"podman", "/run/podman/podman.sock"}}, false},
		{" podman = /run/user/1001/podman/podman.sock , ", "", []engineSocket{{"podman", "/run/user/1001/podman/podman.sock"}}, false},
		{"containerd=/run/containerd.sock", "", nil, true},
		{"docker=", "", nil, true},
		{"/var/run/docker.sock", "", nil, true},
		{"docker=/a,podman=/a", "", nil, true},
		{" , ", "", nil, true},
	}
	for _, c := range cases {
		got, err := parseSockets(c.list, c.single)
		if c.bad {
			if err == nil {
				t.Errorf("%q: expected an error, got %v", c.list, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.list, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.list, got, c.want)
		}
	}
}

func TestLoadConfigSockets(t *testing.T) {
	env := map[string]string{
		"MESHDECK_HOST_NAME": "core-02", "MESHDECK_HOST_ID": "h", "MESHDECK_HOST_TOKEN": "t",
		"MESHDECK_KEY":        "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"MESHDECK_SOCKETS":    "docker=/var/run/docker.sock,podman=/run/podman/podman.sock",
		"MESHDECK_RELAY_HOST": "relay.example",
	}
	cfg, err := loadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.relayHost != "relay.example" || cfg.relayPort != 443 {
		t.Fatalf("relay: %s:%d", cfg.relayHost, cfg.relayPort)
	}
	delete(env, "MESHDECK_RELAY_HOST")
	if _, err := loadConfig(func(k string) string { return env[k] }); err == nil || !contains(err.Error(), "MESHDECK_RELAY_HOST") {
		t.Fatalf("a missing relay host must be named, got %v", err)
	}
	if len(cfg.sockets) != 2 || cfg.sockets[1].engine != "podman" {
		t.Fatalf("sockets: %v", cfg.sockets)
	}
	summary := cfg.redactedSummary()
	if !contains(summary, "docker=/var/run/docker.sock,podman=/run/podman/podman.sock") || contains(summary, "t") == false {
		t.Fatalf("summary: %s", summary)
	}
	if contains(summary, "AAECAw") {
		t.Fatalf("summary leaks the key: %s", summary)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

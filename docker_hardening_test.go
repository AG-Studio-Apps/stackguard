package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIsContainerID(t *testing.T) {
	good := []string{
		strings.Repeat("a", 12), strings.Repeat("0", 64),
		"363dc9b4a947fcbd4b9773e60fae1e3356f8c82ca3e5ecdc37f3c6763c99a0cf",
	}
	for _, id := range good {
		if !isContainerID(id) {
			t.Errorf("%q should be a valid id", id)
		}
	}
	bad := []string{
		"", "short", strings.Repeat("a", 65), // length
		"363dc9b4a947/../../secret", "abc?all=1", "ABCDEF012345", // path/query/uppercase
		"g3dc9b4a947f", "363dc9b4 947f", // non-hex, space
	}
	for _, id := range bad {
		if isContainerID(id) {
			t.Errorf("%q should be rejected", id)
		}
	}
}

func TestDecodeBoundedRejectsOversize(t *testing.T) {
	// A body larger than the cap must fail the decode, not be read whole.
	var out []containerSummary
	huge := bytes.NewReader([]byte("[" + strings.Repeat(`{"Id":"x"},`, (maxResponseBytes/11)+16) + `{"Id":"y"}]`))
	if err := decodeBounded(huge, &out); err == nil {
		t.Fatal("an oversize body should not decode")
	}
	// A small body decodes fine.
	out = nil
	if err := decodeBounded(strings.NewReader(`[{"Id":"abc","State":"running"}]`), &out); err != nil {
		t.Fatalf("small body: %v", err)
	}
	if len(out) != 1 || out[0].ID != "abc" {
		t.Fatalf("decoded wrong: %+v", out)
	}
}

package main

import (
	"encoding/hex"
	"os"
	"reflect"
	"testing"
	"time"
)

func testKey() []byte {
	k, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	p := newPayload("core-02", "podman", "sonarr", "down", "restart loop: exit 137", time.Unix(1800000000, 0))
	blob, err := seal(p, testKey(), "host-abc")
	if err != nil {
		t.Fatal(err)
	}
	got, err := open(blob, testKey(), "host-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip mismatch: %+v != %+v", got, p)
	}
	// Wrong AAD (hostID) must fail — a blob replayed against another host.
	if _, err := open(blob, testKey(), "host-xyz"); err == nil {
		t.Fatal("open with wrong hostID should fail")
	}
}

// TestEmitCrossCheckFixture writes Go-sealed blobs for the Swift AlertCipher
// (swift-crypto) to open, proving the format matches the app end: the
// original vector without an engine, and one that names one.
func TestEmitCrossCheckFixture(t *testing.T) {
	dir := os.Getenv("CROSSCHECK_DIR")
	if dir == "" {
		t.Skip("set CROSSCHECK_DIR to emit the interop fixture")
	}
	for name, engine := range map[string]string{"crosscheck.txt": "", "crosscheck-podman.txt": "podman"} {
		p := newPayload("core-02", engine, "sonarr", "down", "restart loop: exit 137", time.Unix(1800000000, 0))
		blob, err := seal(p, testKey(), "host-abc")
		if err != nil {
			t.Fatal(err)
		}
		out := blob + "\n" + hex.EncodeToString(testKey()) + "\nhost-abc\n" + p.Detail + "\n"
		if err := os.WriteFile(dir+"/"+name, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

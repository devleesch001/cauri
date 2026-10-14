package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEventID(t *testing.T) {
	line := []byte(`{"eventid":"cowrie.session.connect","session":"abc"}`)
	id := EventID("hp-01", line)
	if !ValidSHA256(id) {
		t.Fatalf("EventID = %q, want hex sha256", id)
	}
	if EventID("hp-01", append(line, '\r', '\n')) != id {
		t.Error("line terminator must not change the id")
	}
	if EventID("hp-02", line) == id {
		t.Error("same line on another sensor must give another id")
	}
	if EventID("hp-0", append([]byte("1\n"), line...)) == id {
		t.Error("sensor and line must be separated")
	}
}

func TestDigestRoundTrip(t *testing.T) {
	sum := sha256.Sum256([]byte("tty"))
	hexSum := hex.EncodeToString(sum[:])
	h, err := FormatDigest(hexSum)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseDigest("md5=:AAAA:, " + h)
	if err != nil || got != hexSum {
		t.Fatalf("ParseDigest(%q) = %q, %v", h, got, err)
	}
}

func TestParseDigestErrors(t *testing.T) {
	for _, h := range []string{"", "md5=:AAAA:", "sha-256=abc", "sha-256=:AAAA:"} {
		if _, err := ParseDigest(h); err == nil {
			t.Errorf("ParseDigest(%q): want error", h)
		}
	}
}

func TestValidators(t *testing.T) {
	for s, want := range map[string]bool{
		"hp-01": true, "a1b2c3d4e5f6": true, "": false, "-x": false,
		"a/b": false, "../x": false, "a b": false,
	} {
		if ValidSensorID(s) != want {
			t.Errorf("ValidSensorID(%q) != %v", s, want)
		}
	}
	if ValidSHA256("ABCD") || ValidSHA256(EventID("x", nil)[:10]) {
		t.Error("ValidSHA256 accepted an invalid value")
	}
}

package auth

import (
	"strings"
	"testing"
	"time"
)

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		p := GeneratePassword()
		if len(p) != 64 || strings.Trim(p, passwordAlphabet) != "" {
			t.Fatalf("password %q: want 64 letters and digits", p)
		}
		if seen[p] {
			t.Fatalf("password %q generated twice", p)
		}
		seen[p] = true
	}
}

func TestAdminSessions(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	a := NewAdmin("correct horse")
	a.now = func() time.Time { return now }
	if !a.Check("correct horse") || a.Check("correct hors") || a.Check("") {
		t.Error("password check")
	}

	s, exp := a.NewSession()
	if !exp.Equal(now.Add(AdminSessionTTL)) || !a.ValidSession(s) {
		t.Fatalf("session %q expiring %v not valid", s, exp)
	}
	e, sig, _ := strings.Cut(s, ".")
	for _, bad := range []string{"", "x", e, e + ".", "9999999999." + sig, e + "." + sig + "x", strings.ToUpper(s)} {
		if a.ValidSession(bad) {
			t.Errorf("forged session %q accepted", bad)
		}
	}
	// Another password signs differently: changing it ends sessions.
	other := NewAdmin("another")
	other.now = a.now
	if other.ValidSession(s) {
		t.Error("session valid under another password")
	}
	now = exp
	if a.ValidSession(s) {
		t.Error("expired session accepted")
	}
}

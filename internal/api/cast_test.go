package api

import "testing"

// A simulated replay's prompt is a shell's: the account that logged in on
// its sensor, without what the attacker could make the terminal run.
func TestSimulatedPrompt(t *testing.T) {
	for _, c := range []struct{ user, sensor, want string }{
		{"root", "hp-01", "root@hp-01:~# "},
		{"admin", "hp-01", "admin@hp-01:~$ "},
		{"", "hp-01", "hp-01:~$ "},
		{"ad\x1b]0;owned\x07min\r\n", "hp-01", "ad]0;ownedmin@hp-01:~$ "},
	} {
		if got := simulatedPrompt(c.user, c.sensor); got != c.want {
			t.Errorf("%q on %q: %q, want %q", c.user, c.sensor, got, c.want)
		}
	}
}

package main

import "testing"

func TestRetentionDays(t *testing.T) {
	for in, want := range map[string]retentionDays{"": -1, "null": -1, " NULL ": -1, "-1": -1, "0": 0, "30": 30} {
		r := retentionDays(5)
		if err := r.Set(in); err != nil || r != want {
			t.Errorf("%q: %d, %v; want %d", in, r, err, want)
		}
	}
	for _, in := range []string{"-2", "thirty", "1.5"} {
		r := keepForever
		if err := r.Set(in); err == nil {
			t.Errorf("%q accepted as %d", in, r)
		}
	}
}

package version

import "testing"

func TestString(t *testing.T) {
	for want, i := range map[string]Info{
		"dev": {Tag: "dev"},
		"v1.2.0 (commit 1a2b3c4d5e6f, built 2026-10-16T20:45:00Z)": {
			Tag: "v1.2.0", Commit: "1a2b3c4d5e6f7a8b9c0d", Date: "2026-10-16T20:45:00Z",
		},
		"dev (commit 1a2b3c4d5e6f-dirty)": {Tag: "dev", Commit: "1a2b3c4d5e6f7a8b9c0d", Dirty: true},
	} {
		if got := i.String(); got != want {
			t.Errorf("%+v: %q, want %q", i, got, want)
		}
	}
}

// What the build sets wins over what Go stamped.
func TestGet(t *testing.T) {
	defer func(tag, commit, date string) { Tag, Commit, Date = tag, commit, date }(Tag, Commit, Date)
	Tag, Commit, Date = "", "", ""
	if i := Get(); i.Tag != "dev" {
		t.Errorf("unset tag = %q", i.Tag)
	}
	Tag, Commit, Date = "v1.2.0", "abc", "2026-10-16T20:45:00Z"
	if i := Get(); i.Tag != "v1.2.0" || i.Commit != "abc" || i.Date != "2026-10-16T20:45:00Z" {
		t.Errorf("set = %+v", i)
	}
}

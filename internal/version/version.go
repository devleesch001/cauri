// Package version tells which cauri runs: its release tag, the commit it
// was built from and when it was built. The build sets them with
//
//	-ldflags "-X github.com/devleesch001/cauri/internal/version.Tag=v1.2.0
//	          -X github.com/devleesch001/cauri/internal/version.Commit=<hash>
//	          -X github.com/devleesch001/cauri/internal/version.Date=<RFC 3339>"
//
// (Makefile, Dockerfile, .goreleaser.yaml). A plain go build sets none: the
// commit then comes from what Go stamps in the binary, when it built from
// a git checkout.
package version

import (
	"runtime/debug"
	"strings"
)

// Set at build time; empty when not.
var (
	Tag    = "" // release tag, such as v1.2.0, or git describe's output
	Commit = "" // full git commit hash
	Date   = "" // build time, RFC 3339 in UTC
)

// Info is the version of the running cauri.
type Info struct {
	Tag    string `json:"tag"`    // "dev" when not a release build
	Commit string `json:"commit"` // empty when unknown
	Dirty  bool   `json:"dirty"`  // built from a modified checkout, as Go stamped it
	Date   string `json:"date"`   // empty when unknown
}

// Get returns the version of the running cauri.
func Get() Info {
	i := Info{Tag: Tag, Commit: Commit, Date: Date}
	if i.Tag == "" {
		i.Tag = "dev"
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value
				}
			case "vcs.modified":
				i.Dirty = s.Value == "true"
			}
		}
	}
	return i
}

// String names the version in one line, such as
// "v1.2.0 (commit 1a2b3c4d5e6f, built 2026-10-16T20:45:00Z)".
func (i Info) String() string {
	var details []string
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		if i.Dirty {
			c += "-dirty"
		}
		details = append(details, "commit "+c)
	}
	if i.Date != "" {
		details = append(details, "built "+i.Date)
	}
	if len(details) == 0 {
		return i.Tag
	}
	return i.Tag + " (" + strings.Join(details, ", ") + ")"
}

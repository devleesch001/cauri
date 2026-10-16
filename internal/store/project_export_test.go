package store

import (
	"io/fs"
	"strings"
)

// The migrations written in Go, alone, for the tests of package
// store_test, which cover both engines: 00009 and 00011.
var (
	ProjectUp, ProjectDown       = projectUp, projectDown
	GeoSourcesUp, GeoSourcesDown = geoSourcesUp, geoSourcesDown
)

// MigrationSQL returns the statements of a migration file of an engine,
// such as ("sqlite", "00012_networks.tx.up.sql"), to run one alone.
func MigrationSQL(engine, file string) ([]string, error) {
	b, err := fs.ReadFile(migrations, "migrations/"+engine+"/"+file)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, q := range strings.Split(string(b), "--bun:split") {
		if q = strings.TrimSpace(q); q != "" {
			out = append(out, q)
		}
	}
	return out, nil
}

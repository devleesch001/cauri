package main

import (
	"slices"
	"testing"

	"github.com/devleesch001/cauri/internal/envflag"
)

// The administration page shows the configuration without its secrets.
func TestMaskSecrets(t *testing.T) {
	for dsn, want := range map[string]string{
		"data/cauri.db": "data/cauri.db",
		"postgres://cauri:s3cr%40t@db:5432/cauri?sslmode=disable": "postgres://cauri:****@db:5432/cauri?sslmode=disable",
		"postgres://cauri@db/cauri":                               "postgres://cauri@db/cauri",
		"host=db user=cauri password=s3cret dbname=cauri":         "host=db user=cauri password=**** dbname=cauri",
		"host=db password='a b' dbname=cauri":                     "host=db password=**** dbname=cauri",
	} {
		if got := maskDSN(dsn); got != want {
			t.Errorf("%q: %q, want %q", dsn, got, want)
		}
	}
	opts := maskSecrets([]envflag.Option{
		{Name: "admin-password", Value: "hunter2"},
		{Name: "admin-password-file", Value: "/run/secrets/admin"},
		{Name: "listen", Value: ":8080"},
	})
	if opts[0].Value != "****" || opts[1].Value != "/run/secrets/admin" || opts[2].Value != ":8080" {
		t.Errorf("options = %+v", opts)
	}
}

// One image runs every role: the API does not report the variables the
// ingestor reads, which the image sets for it.
func TestUnknownEnvOfTheImage(t *testing.T) {
	t.Setenv("CAURI_STATE_DIR", "/data/ingestor")
	t.Setenv("CAURI_LISTN", ":9090")
	fs := envflag.New("api")
	fs.String("listen", ":8080", "")
	u := envflag.Unknown(fs, ingestorFlagSet())
	if slices.Contains(u, "CAURI_STATE_DIR") || !slices.Contains(u, "CAURI_LISTN") {
		t.Errorf("unknown = %v", u)
	}
}

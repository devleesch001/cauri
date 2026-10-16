package envflag

import (
	"slices"
	"testing"
	"time"
)

func TestEnvName(t *testing.T) {
	if got := EnvName("db-dsn"); got != "CAURI_DB_DSN" {
		t.Fatalf("EnvName = %q", got)
	}
}

func TestParse(t *testing.T) {
	t.Setenv("CAURI_LISTEN", ":9000")
	t.Setenv("CAURI_BATCH_SIZE", "42")
	t.Setenv("CAURI_FLUSH_INTERVAL", "3s")

	fs := New("test")
	listen := fs.String("listen", ":8080", "")
	batch := fs.Int("batch-size", 500, "")
	flush := fs.Duration("flush-interval", time.Second, "")
	other := fs.String("other", "def", "")

	if err := Parse(fs, []string{"--batch-size", "7"}); err != nil {
		t.Fatal(err)
	}
	if *listen != ":9000" {
		t.Errorf("listen = %q, want value from env", *listen)
	}
	if *batch != 7 {
		t.Errorf("batch-size = %d, want command line to win over env", *batch)
	}
	if *flush != 3*time.Second {
		t.Errorf("flush-interval = %v", *flush)
	}
	if *other != "def" {
		t.Errorf("other = %q, want default", *other)
	}
}

func TestParseInvalidEnv(t *testing.T) {
	t.Setenv("CAURI_BATCH_SIZE", "many")
	fs := New("test")
	fs.Int("batch-size", 500, "")
	if err := Parse(fs, nil); err == nil {
		t.Fatal("want error for invalid env value")
	}
}

func TestParseOnly(t *testing.T) {
	t.Setenv("CAURI_DB_DSN", "from-env")
	t.Setenv("CAURI_IP", "203.0.113.1")

	fs := New("test")
	dsn := fs.String("db-dsn", "def", "")
	ip := fs.String("ip", "", "")
	if err := ParseOnly(fs, nil, "db-dsn"); err != nil {
		t.Fatal(err)
	}
	if *dsn != "from-env" || *ip != "" {
		t.Errorf("db-dsn = %q, ip = %q: want only db-dsn from env", *dsn, *ip)
	}
}

// Describe tells each value's source; Unknown names variables no flag reads.
func TestDescribe(t *testing.T) {
	t.Setenv("CAURI_LISTEN", ":9090")
	t.Setenv("CAURI_LISTN", ":1") // a typo
	fs := New("api")
	fs.String("listen", ":8080", "address")
	fs.String("db-dsn", "data/cauri.db", "database")
	fs.Int("max", 3, "limit")
	if err := Parse(fs, []string{"--db-dsn", "x.db"}); err != nil {
		t.Fatal(err)
	}
	got := map[string]Option{}
	for _, o := range Describe(fs) {
		got[o.Name] = o
	}
	for name, want := range map[string]Option{
		"listen": {Name: "listen", Env: "CAURI_LISTEN", Default: ":8080", Value: ":9090", Source: FromEnv, Usage: "address"},
		"db-dsn": {Name: "db-dsn", Env: "CAURI_DB_DSN", Default: "data/cauri.db", Value: "x.db", Source: FromFlag, Usage: "database"},
		"max":    {Name: "max", Env: "CAURI_MAX", Default: "3", Value: "3", Source: FromDefault, Usage: "limit"},
	} {
		if got[name] != want {
			t.Errorf("%s = %+v, want %+v", name, got[name], want)
		}
	}
	if u := Unknown(fs); !slices.Contains(u, "CAURI_LISTN") || slices.Contains(u, "CAURI_LISTEN") {
		t.Errorf("unknown = %v", u)
	}
}

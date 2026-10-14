package envflag

import (
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

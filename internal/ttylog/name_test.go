package ttylog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Cowrie names a recording after its input: the files it wrote are named
// so (sample of exec channels, see ../ingest/testdata).
func TestName(t *testing.T) {
	paths, err := filepath.Glob("../ingest/testdata/cowrie-v3.1.2-exec-tty/*")
	if err != nil || len(paths) == 0 {
		t.Fatalf("samples: %v %v", paths, err)
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		name, err := Name(f)
		_ = f.Close()
		if err != nil || name != filepath.Base(p) {
			t.Errorf("%s: name = %s, %v", filepath.Base(p), name, err)
		}
	}
	if _, err := Name(bytes.NewReader(rec(OpWrite, DirInput, 0, "ls")[:headerSize+1])); err == nil {
		t.Error("truncated recording named")
	}
}

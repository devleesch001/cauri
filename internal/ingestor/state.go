package ingestor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// loadJSON reads v from path. A missing file leaves v untouched and
// returns false.
func loadJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: file of the configured state directory
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, v)
}

// saveJSON writes v to path atomically: a crash leaves either the old or
// the new content, never a mix.
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

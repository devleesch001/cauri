// Package webui serves the compiled Vue UI, embedded in the binary. Build
// it first with "npm run build" in ui/ (or "make ui"); without it, every
// page answers that the UI is missing.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the UI files. Paths that are not files get index.html, so
// that the client-side router handles them.
func Handler() http.Handler {
	files, _ := fs.Sub(dist, "dist")
	return handler(files)
}

func handler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if fi, err := fs.Stat(files, name); err == nil && !fi.IsDir() && name != "index.html" && name != ".gitkeep" {
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in asset names.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		index, err := fs.ReadFile(files, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "The UI is not part of this build: run \"make ui\" before building cauri.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		_, _ = w.Write(index)
	})
}

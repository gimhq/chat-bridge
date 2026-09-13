package server

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// uiHandler serves the embedded single-page app under /ui/. Hashed assets are cached forever;
// every other path falls back to index.html so client-side routes survive a reload. The page
// itself holds no data: every API call carries the bearer token the user enters.
func uiHandler(ui fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setUIHeaders(w)
		if ui == nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("web UI not built: run `bun install && bun run build` in web/, then rebuild chat-bridge\n"))
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, "/ui")), "/")
		if name != "" && name != "index.html" {
			if data, err := fs.ReadFile(ui, name); err == nil {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
				return
			}
			if path.Ext(name) != "" && strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		data, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	}
}

func setUIHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; "+
		"style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
}

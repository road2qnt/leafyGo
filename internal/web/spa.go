package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// mountSPA serves the built Vite bundle at /app/. The M1 template UI remains at
// / until the M2 JSON API is complete; flip SPA_ROOT later to make this the
// primary shell.
func (s *Server) mountSPA(r chi.Router) {
	spaFS, err := fs.Sub(spaAssets, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(spaFS))

	r.Get("/app", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/app/", http.StatusPermanentRedirect)
	})

	r.Handle("/app/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rel := "/" + strings.TrimPrefix(req.URL.Path, "/app/")
		rel = path.Clean(rel)

		if rel != "/" {
			if info, statErr := fs.Stat(spaFS, strings.TrimPrefix(rel, "/")); statErr == nil && !info.IsDir() {
				if strings.HasPrefix(rel, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				clone := req.Clone(req.Context())
				clone.URL.Path = rel
				fileServer.ServeHTTP(w, clone)
				return
			}
		}

		// A missing hashed asset is a real 404; only client routes fall back to
		// index.html.
		if strings.HasPrefix(rel, "/assets/") || rel == "/favicon.svg" {
			http.NotFound(w, req)
			return
		}
		s.serveSPAIndex(w, req, spaFS)
	}))
}

func (s *Server) serveSPAIndex(w http.ResponseWriter, req *http.Request, spaFS fs.FS) {
	index, err := fs.ReadFile(spaFS, "index.html")
	if err != nil {
		s.logger.Error("spa.index.missing", "error", err)
		http.Error(w, "SPA bundle is not built. Run `npm run build` in web/.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if req.Method != http.MethodHead {
		_, _ = w.Write(index)
	}
}

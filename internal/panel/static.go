package panel

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the built web client: its hashed assets cached for
// good, index.html never cached, and index.html for any path that is not a
// file (the client routes by the URL's fragment, so this is only "/").
func (s *Server) staticHandler() http.Handler {
	if s.static == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("the panel's web client is not built into this binary; build with -tags panelembed\n"))
		})
	}
	files := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(s.static, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "fonts/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		page, err := fs.ReadFile(s.static, "index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
}

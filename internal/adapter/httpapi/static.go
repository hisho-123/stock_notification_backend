package httpapi

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func staticHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cleaned := path.Clean("/" + r.URL.Path)
		for _, part := range strings.Split(cleaned, "/") {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		file := filepath.Join(dir, filepath.FromSlash(cleaned))
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			http.ServeFile(w, r, file)
			return
		}
		// Only application routes fall back to the SPA, not missing JS/CSS assets.
		if cleaned == "/" || cleaned == "/login" || cleaned == "/register" || cleaned == "/assets" || cleaned == "/settings" || cleaned == "/settings/notifications" {
			index := filepath.Join(dir, "index.html")
			if info, err := os.Stat(index); err == nil && info.Mode().IsRegular() {
				http.ServeFile(w, r, index)
				return
			}
		}
		http.NotFound(w, r)
	})
}

package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticFilesAndSPARoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<html>app</html>", "assets/app.js": "console.log('app')", ".env": "private"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := staticHandler(dir)
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<html>app</html>"}, {"/login", 200, "<html>app</html>"}, {"/assets", 200, "<html>app</html>"}, {"/settings", 200, "<html>app</html>"}, {"/settings/notifications", 200, "<html>app</html>"},
		{"/assets/app.js", 200, "console.log('app')"}, {"/missing.js", 404, ""}, {"/.env", 404, ""},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || tc.status == 200 && w.Body.String() != tc.body {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestStaticMethodsAndMissingIndex(t *testing.T) {
	dir := t.TempDir()
	handler := staticHandler(dir)
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/", nil))
		if w.Code != 405 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{"/", "/login", "/assets/.secret"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "index.html"), 0700); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("HEAD", "/", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("script"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("HEAD", "/app.js", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

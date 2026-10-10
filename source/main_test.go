package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalServerRoutes(t *testing.T) {
	g := gameServer{map[string]asset{"/index.html": {[]byte("game"), "text/html"}, "/vendor/core.js": {[]byte("export const ok=true"), "text/javascript"}}, "127.0.0.1:41739", nil}
	cases := []struct {
		method, path, host string
		code               int
		body               string
	}{{"GET", "/", "127.0.0.1:41739", 200, "game"}, {"HEAD", "/", "127.0.0.1:41739", 200, ""}, {"GET", "/_wildlight/health", "127.0.0.1:41739", 200, identity}, {"GET", "/vendor/core.js", "127.0.0.1:41739", 200, "export const ok=true"}, {"GET", "/../source/main.go", "127.0.0.1:41739", 404, ""}, {"GET", "/source/main.go", "127.0.0.1:41739", 404, ""}, {"GET", "/", "attacker.example", 403, ""}, {"POST", "/", "127.0.0.1:41739", 405, ""}}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != c.code {
			t.Errorf("%s %s got %d want %d", c.method, c.path, w.Code, c.code)
		}
		if c.code == 200 && w.Body.String() != c.body {
			t.Errorf("incorrect content %s", c.path)
		}
		if c.code == 200 && w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing content-type guard")
		}
	}
}
func TestAssetBoundary(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "app"), 0700)
	_ = os.WriteFile(filepath.Join(root, "app/index.html"), []byte("game"), 0600)
	_ = os.WriteFile(filepath.Join(root, "private.txt"), []byte("outside"), 0600)
	assets, err := loadAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatal("outside package app was exposed")
	}
	if err := os.Symlink(filepath.Join(root, "private.txt"), filepath.Join(root, "app/link.txt")); err == nil {
		if _, err = loadAssets(root); err == nil {
			t.Fatal("symlink was accepted")
		}
	}
}
func TestMissingGame(t *testing.T) {
	if _, err := loadAssets(t.TempDir()); err == nil {
		t.Fatal("missing app was accepted")
	}
}

var _ = http.MethodGet

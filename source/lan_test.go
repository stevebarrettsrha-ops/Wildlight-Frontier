package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func apiTest(t *testing.T, g *lanServer, path, token string, in any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(in)
	r := httptest.NewRequest("POST", "http://127.0.0.1:41739/api/"+path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	g.api(w, r)
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e, w.Body.String())
	}
	return w.Code, out
}
func joinTest(t *testing.T, g *lanServer, name string) string {
	t.Helper()
	code, v := apiTest(t, g, "join", "", map[string]string{"name": name, "code": g.world.Code})
	if code != 200 {
		t.Fatal(code, v)
	}
	return v["token"].(string)
}
func turnTest(t *testing.T, g *lanServer, token string) map[string]any {
	t.Helper()
	code, v := apiTest(t, g, "acquire", token, map[string]any{"rev": 0, "state": map[string]any{"realm": "overworld", "active": true, "pos": []int{1, 2, 3}}})
	if code != 200 {
		t.Fatal(code, v)
	}
	return v
}
func TestLANTransactionsAndDisk(t *testing.T) {
	root := t.TempDir()
	g, e := newLAN(root, 41739)
	if e != nil {
		t.Fatal(e)
	}
	a := joinTest(t, g, "Alice")
	b := joinTest(t, g, "Bob")
	for _, name := range []string{"Third", "Alice"} {
		if c, _ := apiTest(t, g, "join", "", map[string]string{"name": name, "code": g.world.Code}); c != 409 {
			t.Fatal("extra/duplicate player admitted")
		}
	}
	first := turnTest(t, g, a)
	if c, _ := apiTest(t, g, "acquire", b, map[string]any{"rev": 0}); c != 423 {
		t.Fatal("simultaneous lease admitted")
	}
	save := map[string]any{"game": "Wildlight Frontier", "seed": g.world.Seed, "inv": make([]any, 36), "player": "Alice"}
	patch := map[string]any{"overworld/block/1,2,3": 4, "overworld/chest/1,2,3": []any{map[string]int{"id": 4, "n": 3}}, "overworld/drop/test": map[string]int{"id": 4, "n": 1}}
	body := map[string]any{"lease": first["lease"], "rev": first["rev"], "patch": patch, "save": save, "flush": true, "settings": map[string]string{"graphics": "8gb"}}
	if c, _ := apiTest(t, g, "commit", a, body); c != 200 {
		t.Fatal("commit", c)
	}
	if c, v := apiTest(t, g, "commit", a, body); c != 200 || v["duplicate"] != true {
		t.Fatal("idempotency", c, v)
	}
	second := turnTest(t, g, b)
	if len(second["changes"].([]any)) != 3 {
		t.Fatal("missing world edits")
	}
	body = map[string]any{"lease": second["lease"], "rev": second["rev"], "patch": map[string]any{"overworld/drop/test": nil, "overworld/chest/1,2,3": []any{nil}}, "flush": true, "save": map[string]any{"game": "Wildlight Frontier", "seed": g.world.Seed, "inv": make([]any, 36), "player": "Bob"}}
	if c, _ := apiTest(t, g, "commit", b, body); c != 200 {
		t.Fatal(c)
	}
	if _, ok := g.world.Shared["overworld/drop/test"]; ok {
		t.Fatal("drop not removed")
	}
	if c, _ := apiTest(t, g, "commit", a, body); c != 409 {
		t.Fatal("foreign commit allowed")
	}
	next := turnTest(t, g, a)
	g.until = time.Now().Add(-time.Second)
	if c, _ := apiTest(t, g, "commit", a, map[string]any{"lease": next["lease"], "rev": next["rev"], "patch": patch}); c != 409 {
		t.Fatal("expired lease accepted")
	}
	if c, _ := apiTest(t, g, "leave", a, map[string]any{}); c != 200 {
		t.Fatal(c)
	}
	a = joinTest(t, g, "Alice")
	if c, v := apiTest(t, g, "bootstrap", a, map[string]any{}); c != 200 || v["profile"].(map[string]any)["name"] != "Alice" {
		t.Fatal("reconnect", c, v)
	}
	if e = g.flush(); e != nil {
		t.Fatal(e)
	}
	reload, e := newLAN(root, 41739)
	if e != nil {
		t.Fatal(e)
	}
	if len(reload.world.Players) != 2 || len(reload.world.Shared) != 2 {
		t.Fatal("disk mismatch")
	}
	for _, p := range reload.world.Players {
		if len(p.Save) == 0 {
			t.Fatal("missing private inventory")
		}
	}
	if _, e = os.Stat(filepath.Join(root, "Saves", "world.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "Saves", "world.json.tmp")); !os.IsNotExist(e) {
		t.Fatal("temporary file leaked")
	}
}
func TestLANValidationAndImport(t *testing.T) {
	g, e := newLAN(t.TempDir(), 41739)
	if e != nil {
		t.Fatal(e)
	}
	if c, _ := apiTest(t, g, "join", "", map[string]string{"name": "Alice", "code": "WRONG!"}); c != 403 {
		t.Fatal("code bypass")
	}
	a := joinTest(t, g, "Alice")
	tr := turnTest(t, g, a)
	if c, _ := apiTest(t, g, "commit", a, map[string]any{"lease": tr["lease"], "rev": tr["rev"], "patch": map[string]int{"../../outside": 1}}); c != 400 {
		t.Fatal("path accepted")
	}
	g.until = time.Now().Add(-time.Second)
	if c, v := apiTest(t, g, "import", a, map[string]any{"seed": 123, "save": map[string]any{"game": "Wildlight Frontier", "inv": make([]any, 36)}, "shared": map[string]any{"overworld/block/1,2,3": 5}}); c != 200 {
		t.Fatal("import", c, v)
	}
	backups, _ := filepath.Glob(filepath.Join(g.root, "before-import-*.json"))
	if len(backups) != 1 {
		t.Fatal("missing import backup")
	}
	if g.world.Seed != 123 {
		t.Fatal("seed not imported")
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:41739/api/join", bytes.NewBufferString(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	g.api(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	_ = os.WriteFile(filepath.Join(g.root, "world.json"), []byte("broken"), 0600)
	if _, e = newLAN(filepath.Dir(g.root), 41739); e == nil {
		t.Fatal("corrupt save overwritten")
	}
}

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type profile struct {
	Name     string            `json:"name"`
	Save     json.RawMessage   `json:"save"`
	Settings map[string]string `json:"settings"`
}
type diskWorld struct {
	Version int                        `json:"version"`
	Seed    uint32                     `json:"seed"`
	Code    string                     `json:"code"`
	Rev     uint64                     `json:"rev"`
	Shared  map[string]json.RawMessage `json:"shared"`
	Players map[string]*profile        `json:"players"`
}
type presence struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	State json.RawMessage `json:"state"`
}
type session struct {
	ID, Token  string
	Seen       time.Time
	State      json.RawMessage
	LastCommit string
	LastRev    uint64
}
type change struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	Rev   uint64          `json:"rev"`
}
type lanServer struct {
	mu           sync.Mutex
	root         string
	world        diskWorld
	sessions     map[string]*session
	history      []change
	owner, lease string
	until        time.Time
	dirty        bool
	lastBackup   time.Time
	saveError    string
	hosts        map[string]bool
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func newLAN(root string, port int) (*lanServer, error) {
	g := &lanServer{root: filepath.Join(root, "Saves"), sessions: map[string]*session{}, hosts: map[string]bool{fmt.Sprintf("127.0.0.1:%d", port): true, fmt.Sprintf("localhost:%d", port): true}}
	if err := os.MkdirAll(g.root, 0700); err != nil {
		return nil, fmt.Errorf("cannot create Saves folder: %w", err)
	}
	ips, _ := net.InterfaceAddrs()
	for _, a := range ips {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip != nil && ip.To4() != nil {
			g.hosts[net.JoinHostPort(ip.String(), fmt.Sprint(port))] = true
		}
	}
	b, err := os.ReadFile(filepath.Join(g.root, "world.json"))
	if err == nil {
		if err = json.Unmarshal(b, &g.world); err != nil || g.world.Version != 1 || g.world.Shared == nil || g.world.Players == nil {
			return nil, errors.New("Saves/world.json is damaged or unsupported. Restore world.previous.json after making a copy of Saves; existing files were not overwritten")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		g.world = diskWorld{Version: 1, Seed: 1847263091, Code: strings.ToUpper(randomID(3)), Shared: map[string]json.RawMessage{}, Players: map[string]*profile{}}
		g.dirty = true
		if err = g.flushLocked(); err != nil {
			return nil, err
		}
	}
	return g, nil
}
func (g *lanServer) flushLocked() error {
	if !g.dirty {
		return nil
	}
	b, err := json.Marshal(g.world)
	if err != nil {
		return err
	}
	path := filepath.Join(g.root, "world.json")
	if time.Since(g.lastBackup) > 60*time.Second {
		if old, e := os.ReadFile(path); e == nil {
			if e = os.WriteFile(filepath.Join(g.root, "world.previous.json"), old, 0600); e != nil {
				return e
			}
		}
		g.lastBackup = time.Now()
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		g.saveError = err.Error()
		return err
	}
	if d, e := os.Open(g.root); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	g.dirty = false
	g.saveError = ""
	return nil
}
func (g *lanServer) flush() error { g.mu.Lock(); defer g.mu.Unlock(); return g.flushLocked() }
func (g *lanServer) expire() {
	now := time.Now()
	for t, s := range g.sessions {
		if now.Sub(s.Seen) > 15*time.Second {
			delete(g.sessions, t)
		}
	}
	if now.After(g.until) {
		g.owner = ""
		g.lease = ""
	}
}
func (g *lanServer) peers() []presence {
	out := []presence{}
	for _, s := range g.sessions {
		out = append(out, presence{s.ID, g.world.Players[s.ID].Name, s.State})
	}
	return out
}
func (g *lanServer) updates(rev uint64) []change {
	out := []change{}
	for _, c := range g.history {
		if c.Rev > rev {
			out = append(out, c)
		}
	}
	return out
}
func sendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	sendJSON(w, status, map[string]any{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	if e := d.Decode(v); e != nil {
		fail(w, 400, "Invalid or oversized request.")
		return false
	}
	return true
}

var namePattern = regexp.MustCompile(`^[\pL\pN _-]{1,24}$`)
var keyPattern = regexp.MustCompile(`^(overworld|nether|end)/[a-z]+/[a-zA-Z0-9,._:-]{1,100}$`)

func (g *lanServer) api(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expire()
	if r.URL.Path == "/api/info" && r.Method == "GET" {
		ips := []string{}
		for h := range g.hosts {
			if !strings.HasPrefix(h, "localhost:") && !strings.HasPrefix(h, "127.") {
				ips = append(ips, "http://"+h+"/launcher.html")
			}
		}
		v := map[string]any{"players": len(g.sessions), "capacity": 2, "urls": ips, "seed": g.world.Seed}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if net.ParseIP(host).IsLoopback() {
			v["code"] = g.world.Code
		}
		sendJSON(w, 200, v)
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "Use POST for game APIs.")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "JSON required.")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host || u.Scheme != "http" {
			fail(w, 403, "Use this server's own game page.")
			return
		}
	}
	if r.URL.Path == "/api/join" {
		var in struct{ Name, Code string }
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !namePattern.MatchString(in.Name) {
			fail(w, 400, "Use 1–24 letters, numbers, spaces, underscores or hyphens for your name.")
			return
		}
		if strings.ToUpper(strings.TrimSpace(in.Code)) != g.world.Code {
			fail(w, 403, "Room code does not match the host launcher.")
			return
		}
		sum := sha256.Sum256([]byte(strings.ToLower(in.Name)))
		id := hex.EncodeToString(sum[:8])
		for _, s := range g.sessions {
			if s.ID == id {
				fail(w, 409, "That player is connected. Use another name, or wait 15 seconds after closing the old tab.")
				return
			}
		}
		if len(g.sessions) >= 2 {
			fail(w, 409, "Both player slots are occupied.")
			return
		}
		if g.world.Players[id] == nil {
			g.world.Players[id] = &profile{Name: in.Name, Settings: map[string]string{}}
			g.dirty = true
		}
		token := randomID(24)
		g.sessions[token] = &session{ID: id, Token: token, Seen: time.Now(), State: json.RawMessage(`null`)}
		sendJSON(w, 200, map[string]any{"token": token, "id": id, "seed": g.world.Seed})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s := g.sessions[token]
	if s == nil {
		fail(w, 401, "Session ended. Rejoin with the same player name to resume your inventory.")
		return
	}
	s.Seen = time.Now()
	switch r.URL.Path {
	case "/api/bootstrap":
		sendJSON(w, 200, map[string]any{"id": s.ID, "seed": g.world.Seed, "rev": g.world.Rev, "profile": g.world.Players[s.ID], "shared": g.world.Shared, "peers": g.peers()})
	case "/api/acquire":
		var in struct {
			Rev   uint64
			State json.RawMessage
		}
		if !decode(w, r, &in) {
			return
		}
		if len(in.State) < 4096 && json.Valid(in.State) {
			s.State = in.State
		}
		if g.owner != "" {
			sendJSON(w, 423, map[string]any{"busy": true, "peers": g.peers()})
			return
		}
		g.owner = token
		g.lease = randomID(12)
		g.until = time.Now().Add(5 * time.Second)
		v := map[string]any{"lease": g.lease, "rev": g.world.Rev, "changes": g.updates(in.Rev), "peers": g.peers(), "saveError": g.saveError}
		if in.Rev > g.world.Rev || len(g.history) == 0 && in.Rev < g.world.Rev || len(g.history) > 0 && in.Rev+1 < g.history[0].Rev {
			v["shared"] = g.world.Shared
			v["changes"] = []change{}
		}
		sendJSON(w, 200, v)
	case "/api/commit":
		var in struct {
			Lease    string
			Rev      uint64
			Patch    map[string]json.RawMessage
			Save     json.RawMessage
			Settings map[string]string
			Flush    bool
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Lease != "" && in.Lease == s.LastCommit {
			sendJSON(w, 200, map[string]any{"rev": s.LastRev, "duplicate": true})
			return
		}
		if g.owner != token || g.lease != in.Lease || in.Lease == "" || in.Rev != g.world.Rev {
			fail(w, 409, "World turn expired. Reconnect to restore the committed world.")
			return
		}
		if len(in.Patch) > 250000 {
			fail(w, 400, "World patch too large.")
			return
		}
		for k, v := range in.Patch {
			if !keyPattern.MatchString(k) || len(v) > 2<<20 || !json.Valid(v) {
				fail(w, 400, "Invalid shared world data.")
				return
			}
		}
		if len(in.Save) > 0 {
			var check struct {
				Game string
				Seed uint32
				Inv  []json.RawMessage
			}
			if json.Unmarshal(in.Save, &check) != nil || check.Game != "Wildlight Frontier" || check.Seed != g.world.Seed || len(check.Inv) != 36 {
				fail(w, 400, "Invalid player save.")
				return
			}
		}
		for k, v := range in.Settings {
			if len(k) > 100 || len(v) > 65536 {
				fail(w, 400, "Invalid settings.")
				return
			}
		}
		if len(in.Patch) > 0 {
			g.world.Rev++
			for k, v := range in.Patch {
				if string(v) == "null" {
					delete(g.world.Shared, k)
				} else {
					g.world.Shared[k] = append(json.RawMessage{}, v...)
				}
				g.history = append(g.history, change{k, v, g.world.Rev})
			}
			if len(g.history) > 30000 {
				g.history = g.history[len(g.history)-30000:]
				first := g.history[0].Rev
				for len(g.history) > 0 && g.history[0].Rev == first {
					g.history = g.history[1:]
				}
			}
		}
		p := g.world.Players[s.ID]
		if len(in.Save) > 0 {
			p.Save = in.Save
		}
		if in.Settings != nil {
			p.Settings = in.Settings
		}
		g.dirty = true
		g.owner = ""
		g.lease = ""
		s.LastCommit = in.Lease
		s.LastRev = g.world.Rev
		if in.Flush {
			if e := g.flushLocked(); e != nil {
				fail(w, 507, "Cannot write Saves/world.json: "+e.Error())
				return
			}
		}
		sendJSON(w, 200, map[string]any{"rev": g.world.Rev, "saved": in.Flush})
	case "/api/leave":
		delete(g.sessions, token)
		if g.owner == token {
			g.owner = ""
			g.lease = ""
		}
		if e := g.flushLocked(); e != nil {
			fail(w, 507, e.Error())
			return
		}
		sendJSON(w, 200, map[string]bool{"ok": true})
	case "/api/import":
		if len(g.sessions) > 1 || g.owner != "" {
			fail(w, 409, "Import requires one connected player and no pending world turn. Return to the launcher first.")
			return
		}
		var in struct {
			Save   json.RawMessage
			Shared map[string]json.RawMessage
			Seed   uint32
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Seed == 0 || len(in.Shared) > 250000 {
			fail(w, 400, "Invalid world backup.")
			return
		}
		var check struct {
			Game string
			Inv  []json.RawMessage
		}
		if json.Unmarshal(in.Save, &check) != nil || check.Game != "Wildlight Frontier" || len(check.Inv) != 36 {
			fail(w, 400, "Invalid backup.")
			return
		}
		for k, v := range in.Shared {
			if !keyPattern.MatchString(k) || !json.Valid(v) {
				fail(w, 400, "Invalid backup data.")
				return
			}
		}
		if e := g.flushLocked(); e != nil {
			fail(w, 507, e.Error())
			return
		}
		old, _ := os.ReadFile(filepath.Join(g.root, "world.json"))
		if e := os.WriteFile(filepath.Join(g.root, "before-import-"+time.Now().Format("20060102-150405")+".json"), old, 0600); e != nil {
			fail(w, 507, e.Error())
			return
		}
		for id, p := range g.world.Players {
			if id != s.ID {
				p.Save = nil
			}
		}
		g.world.Seed = in.Seed
		g.world.Shared = in.Shared
		g.world.Rev++
		g.history = nil
		g.world.Players[s.ID].Save = in.Save
		g.dirty = true
		if e := g.flushLocked(); e != nil {
			fail(w, 507, e.Error())
			return
		}
		sendJSON(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, "Unknown game API.")
	}
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const identity = "wildlight-frontier-lan-v2"
const defaultPort = 41739

type asset struct {
	data []byte
	kind string
}
type gameServer struct {
	assets map[string]asset
	host   string
	lan    *lanServer
}

func loadAssets(root string) (map[string]asset, error) {
	app := filepath.Join(root, "app")
	files := make(map[string]asset)
	err := filepath.WalkDir(app, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink in app: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(app, path)
		if err != nil {
			return err
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".html" && ext != ".js" && ext != ".json" && ext != ".png" && ext != ".svg" && ext != ".ico" && ext != ".css" && ext != ".txt" {
			return nil
		}
		stat, err := d.Info()
		if err != nil {
			return err
		}
		if stat.Size() > 32<<20 {
			return fmt.Errorf("unexpected large app asset: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		kind := mime.TypeByExtension(ext)
		if ext == ".js" {
			kind = "text/javascript; charset=utf-8"
		}
		if ext == ".html" {
			kind = "text/html; charset=utf-8"
		}
		files["/"+filepath.ToSlash(rel)] = asset{data, kind}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := files["/index.html"]; !ok {
		return nil, errors.New("app/index.html is missing; extract the entire ZIP before launching")
	}
	return files, nil
}
func (g gameServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (g.lan == nil && r.Host != g.host) || (g.lan != nil && !g.lan.hosts[r.Host]) {
		http.Error(w, "This server accepts only its local address.", http.StatusForbidden)
		return
	}
	if g.lan != nil && strings.HasPrefix(r.URL.Path, "/api/") {
		g.lan.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Read-only game server.", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' data: blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self' blob:; media-src 'self' blob: data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	if r.URL.Path == "/_wildlight/health" {
		w.Header().Set("Content-Type", "text/plain")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, identity)
		}
		return
	}
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	a, ok := g.assets[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.kind)
	w.Header().Set("Content-Length", strconv.Itoa(len(a.data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(a.data)
	}
}
func browser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
func existing(url string) bool {
	client := &http.Client{Timeout: time.Second}
	r, err := client.Get(url + "/_wildlight/health")
	if err != nil {
		return false
	}
	defer r.Body.Close()
	var word string
	_, err = fmt.Fscan(r.Body, &word)
	return err == nil && r.StatusCode == 200 && word == identity
}
func rootDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "runtime" {
		return filepath.Dir(dir)
	}
	return dir
}
func run() error {
	root := flag.String("root", rootDir(), "Folder containing app (normally detected automatically)")
	port := flag.Int("port", defaultPort, "LAN server port (saves remain in the game folder)")
	noBrowser := flag.Bool("no-browser", false, "Run without opening the browser")
	flag.Parse()
	if *port < 1024 || *port > 65535 {
		return errors.New("port must be 1024 through 65535")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(*port))
	base := "http://" + addr
	url := base + "/launcher.html"
	assets, err := loadAssets(*root)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(*port)))
	if err != nil {
		if existing(base) {
			fmt.Println("Wildlight is already running:", url)
			if !*noBrowser {
				_ = browser(url)
			}
			return nil
		}
		return fmt.Errorf("port %d is occupied by another application. Close that application, then try again. Your Saves folder is unaffected. Details: %w", *port, err)
	}
	lan, err := newLAN(*root, *port)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer lan.flush()
	fmt.Println("WILDLIGHT FRONTIER - OFFLINE DESKTOP / TWO-PLAYER LAN")
	fmt.Println("Local address:", url)
	fmt.Println("Choose the 8 GB profile for your RTX 4060, or the lighter 4 GB profile.")
	fmt.Println("Keep this window open while playing. Press Ctrl+C to stop the local server.")
	fmt.Println("Room code:", lan.world.Code)
	for host := range lan.hosts {
		if !strings.HasPrefix(host, "127.") && !strings.HasPrefix(host, "localhost:") {
			fmt.Println("Second player:", "http://"+host+"/launcher.html")
		}
	}
	fmt.Println("Saves:", lan.root, "- keep the same player name to resume your inventory.")
	server := &http.Server{Handler: gameServer{assets: assets, host: addr, lan: lan}, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 16}
	flushStop := make(chan struct{})
	defer close(flushStop)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if e := lan.flush(); e != nil {
					fmt.Println("SAVE ERROR:", e)
				}
			case <-flushStop:
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	if !*noBrowser {
		if err := browser(url); err != nil {
			fmt.Println("Could not open the browser automatically. Open the local address above.")
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Could not start Wildlight:", err)
		if runtime.GOOS == "windows" {
			fmt.Println("Press Enter to close.")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}
}

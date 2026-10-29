package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/nonce"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
	"deck2/internal/release"
	"deck2/internal/sandbox"
	"deck2/internal/tsnet"
	"deck2/web"
)

func main() {
	if err := cleanEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "deck: restart with a clean environment:", err)
		os.Exit(1)
	}
	restart, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
	switch {
	case restart == nil:
	case restart.Launcher:
		os.Exit(release.LauncherCode)
	default:
		// deck stopped whole; it starts again in the new binary, or in the old one when that will not start.
		err := syscall.Exec(restart.Exe, os.Args, os.Environ())
		if rerr := os.Rename(restart.Old, restart.Exe); rerr == nil {
			err = syscall.Exec(restart.Exe, os.Args, os.Environ())
		}
		fmt.Fprintln(os.Stderr, "deck: restart after the update:", err)
		os.Exit(1)
	}
}

func run() (*release.Restart, error) {
	addr := flag.String("addr", cmp.Or(os.Getenv("DECK_ADDR"), "127.0.0.1:7700"), "listen address; the next port is the second origin of custom blocks")
	tailnet := flag.String("tailscale", os.Getenv("DECK_TAILSCALE"), "join the owner's tailnet under this name and serve HTTPS there")
	public := flag.String("public", os.Getenv("DECK_PUBLIC"), "the https address of a proxy deck is reached through, such as https://deck.example.com; its port 8443 is the second origin")
	version := flag.Bool("version", false, "print the version, commit and build of deck and quit")
	flag.Parse()
	if *version {
		fmt.Println(release.Version, release.Commit, release.Variant(tsnet.Built))
		return nil, nil
	}
	if *tailnet != "" && !tsnet.Built {
		return nil, errors.New("deck: -tailscale needs a deck built with go build -tags tailnet")
	}
	if *public != "" && !strings.HasPrefix(*public, "https://") {
		return nil, errors.New("deck: -public is an https address, such as https://deck.example.com")
	}
	if err := sandbox.AgentUserErr(); err != nil {
		return nil, err
	}
	// With a user of agents what deck writes is its own, so that user reads only what deck hands it.
	if sandbox.HasAgentUser() {
		syscall.Umask(0o077)
	}

	home := os.Getenv("DECK_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(h, ".deck2")
	}
	// A second deck of the home would take the agents of the first for those a crash left, and kill them.
	lock, err := lockHome(deckhome.Layout(home))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if sandbox.HasAgentUser() {
		if err := private(deckhome.Layout(home)); err != nil {
			return nil, err
		}
	}
	if err := pending(deckhome.Layout(home)); err != nil {
		return nil, err
	}
	ln, sideLn, err := listenPair(*addr)
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	restarts := make(chan release.Restart, 1)
	a, err := app.New(app.Config{
		Home: home, MCPBase: mcpBase(ln.Addr().(*net.TCPAddr)),
		QuestionWait: 30 * time.Minute, ConfirmWait: 30 * time.Minute, ConfirmTTL: 24 * time.Hour,
		Variant: release.Variant(tsnet.Built), Launcher: os.Getenv("DECK_LAUNCHER") != "", Image: os.Getenv("DECK_IMAGE"),
		Restart: func(r release.Restart) {
			select {
			case restarts <- r:
			default:
			}
		},
	}, claudecode.New(home), codex.New(home))
	if err != nil {
		return nil, err
	}
	defer a.Close()
	token, err := loadToken(a.Home.Token())
	if err != nil {
		return nil, err
	}

	// An update ends deck as a signal does, the signals still heard meanwhile.
	base, end := context.WithCancel(context.Background())
	defer end()
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restart atomic.Pointer[release.Restart]
	go func() {
		select {
		case r := <-restarts:
			restart.Store(&r)
			end()
		case <-ctx.Done():
		}
	}()

	// deck's MCP server answers before any turn starts, a resumed one too; the rest waits until deck has started.
	server := api.New(a, token, web.FS())
	server.Pair(port, port+1)
	if *tailnet != "" {
		server.Pair(tsnet.Ports(false))
	}
	if *public != "" {
		server.Proxied()
		server.Pair(443, 8443)
	}
	var started atomic.Bool
	handler := server.Handler()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !started.Load() && !strings.HasPrefix(r.URL.Path, "/mcp/") {
			http.Error(w, "deck is starting", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	})}
	side := &http.Server{Handler: server.Sandbox()}
	served := make(chan error, 2)
	go func() { served <- side.Serve(sideLn) }()
	go func() { served <- srv.Serve(ln) }()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		a.Stop()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = side.Shutdown(sctx)
		_ = srv.Shutdown(sctx)
	}()

	a.Run(ctx, 30*time.Second)
	started.Store(true)

	if *tailnet != "" {
		node, err := tsnet.Start(ctx, tsnet.Config{Hostname: *tailnet, Dir: a.Home.Tailscale(), Health: a.TailnetHealth}, server)
		if err != nil {
			return nil, fmt.Errorf("tailscale: %w", err)
		}
		defer node.Close()
	}
	// The update this deck is has served, so the binary before it goes.
	if p, ok := release.ReadPending(a.Home.Update()); ok {
		os.Remove(p.Old)
		os.Remove(a.Home.Update())
	}
	url := fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token)
	if *public != "" {
		url = strings.TrimSuffix(*public, "/") + "/?token=" + token
	}
	slog.Info("deck listening", "url", url, "home", home)
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, err
	}
	// Serve returns once Shutdown starts; the calls still running finish before deck closes.
	<-stopped
	return restart.Load(), nil
}

// cleanEnv restarts deck with only the variables it needs: on macOS a confined program reads the
// environment of every process of the owner.
func cleanEnv() error {
	// Go adds GOTRACEBACK to the environment of a binary with capabilities, as deck's in its image.
	clean := sandbox.Kept("HOME", "TMPDIR", "XDG_", "CODEX_HOME", "DECK_HOME", "DECK_LAUNCHER", "DECK_ADDR", "DECK_TAILSCALE",
		"DECK_PUBLIC", "DECK_AGENT_USER", "DECK_SANDBOX", "DECK_IMAGE", "DECK_BRIDGE", "GOTRACEBACK")
	if len(clean) == len(os.Environ()) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, clean)
}

// pending starts the binary before an update when the updated deck already tried to start and never served; a
// first start of the update marks it tried.
func pending(h deckhome.Layout) error {
	p, ok := release.ReadPending(h.Update())
	switch {
	case !ok:
		return nil
	case !p.Tried:
		p.Tried = true
		return release.WritePending(h.Update(), p)
	}
	slog.Error("the update did not start; deck goes back to the binary before it", "version", p.Version)
	os.Remove(h.Update())
	if err := os.Rename(p.Old, p.Exe); err != nil {
		return fmt.Errorf("go back from the update %s: %w", p.Version, err)
	}
	return syscall.Exec(p.Exe, os.Args, os.Environ())
}

// private closes to others all deck kept in its state before it had a user of agents, the base too.
func private(h deckhome.Layout) error {
	return filepath.WalkDir(h.State(), func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.Type()&fs.ModeSymlink != 0 {
			return err
		}
		fi, err := e.Info()
		if err == nil && fi.Mode().Perm()&0o077 != 0 {
			err = os.Chmod(p, fi.Mode().Perm()&^0o077)
		}
		return err
	})
}

// lockHome holds the lock of the home until deck exits; another deck holding it refuses this one.
func lockHome(h deckhome.Layout) (*os.File, error) {
	if err := os.MkdirAll(h.State(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(h.Lock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deck runs on %s; one home has one deck", string(h))
	}
	return f, nil
}

func loadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if tok := strings.TrimSpace(string(b)); err == nil && len(tok) >= 32 {
		return tok, os.Chmod(path, 0o600)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tok := nonce.Hex(24)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, os.Chmod(path, 0o600)
}

// mcpBase is where agents of this machine reach deck's MCP servers: loopback when deck listens on every address, as
// the MCP handler refuses a Host such as 0.0.0.0.
func mcpBase(a *net.TCPAddr) string {
	if a.IP.IsUnspecified() {
		return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Port))
	}
	return "http://" + a.String()
}

// listenPair listens on addr and on the next port, the second origin of custom blocks; with any port it tries pairs
// until both are free.
func listenPair(addr string) (net.Listener, net.Listener, error) {
	for try := 0; ; try++ {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, nil, err
		}
		a := ln.Addr().(*net.TCPAddr)
		side, err := net.Listen("tcp", net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port+1)))
		if err == nil {
			return ln, side, nil
		}
		_ = ln.Close()
		if _, p, _ := net.SplitHostPort(addr); p != "0" || try == 20 {
			return nil, nil, fmt.Errorf("second origin for custom blocks: %w", err)
		}
	}
}

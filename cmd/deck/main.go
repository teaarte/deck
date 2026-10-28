package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"deck2/internal/sandbox"
	"deck2/internal/tsnet"
	"deck2/web"
)

func main() {
	if err := cleanEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "deck: restart with a clean environment:", err)
		os.Exit(1)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:7700", "listen address; the next port is the second origin of custom blocks")
	tailnet := flag.String("tailscale", "", "join the owner's tailnet under this name and serve HTTPS there")
	flag.Parse()
	if *tailnet != "" && !tsnet.Built {
		return errors.New("deck: -tailscale needs a deck built with go build -tags tailnet")
	}

	home := os.Getenv("DECK_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(h, ".deck2")
	}
	// A second deck of the home would take the agents of the first for those a crash left, and kill them.
	lock, err := lockHome(deckhome.Layout(home))
	if err != nil {
		return err
	}
	defer lock.Close()
	ln, sideLn, err := listenPair(*addr)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	a, err := app.New(app.Config{
		Home: home, MCPBase: mcpBase(ln.Addr().(*net.TCPAddr)),
		QuestionWait: 30 * time.Minute, ConfirmWait: 30 * time.Minute, ConfirmTTL: 24 * time.Hour,
	}, claudecode.New(home), codex.New(home))
	if err != nil {
		return err
	}
	defer a.Close()
	token, err := loadToken(a.Home.Token())
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// deck's MCP server answers before any turn starts, a resumed one too; the rest waits until deck has started.
	server := api.New(a, token, web.FS())
	server.Pair(port, port+1)
	if *tailnet != "" {
		server.Pair(tsnet.Ports(false))
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
			return fmt.Errorf("tailscale: %w", err)
		}
		defer node.Close()
	}
	slog.Info("deck listening", "url", fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token), "home", home)
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Serve returns once Shutdown starts; the calls still running finish before deck closes.
	<-stopped
	return nil
}

// cleanEnv restarts deck with only the variables it needs: on macOS a confined program reads the
// environment of every process of the owner.
func cleanEnv() error {
	clean := sandbox.Kept("HOME", "TMPDIR", "XDG_", "CODEX_HOME", "DECK_HOME")
	if len(clean) == len(os.Environ()) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, clean)
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

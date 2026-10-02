package main

import (
	"context"
	deckhome "deck2/internal/home"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/nonce"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
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

	home := os.Getenv("DECK_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(h, ".deck2")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(deckhome.Layout(home).State(), 0o700); err != nil {
		return err
	}
	token, err := loadToken(deckhome.Layout(home).Token())
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	sideLn, err := net.Listen("tcp", net.JoinHostPort(ln.Addr().(*net.TCPAddr).IP.String(), strconv.Itoa(port+1)))
	if err != nil {
		return fmt.Errorf("second origin for custom blocks: %w", err)
	}
	a, err := app.New(app.Config{
		Home: home, MCPBase: "http://" + ln.Addr().String(),
		QuestionWait: 30 * time.Minute, ConfirmWait: 30 * time.Minute, ConfirmTTL: 24 * time.Hour,
	}, claudecode.New(home), codex.New(home))
	if err != nil {
		return err
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Reconcile(ctx); err != nil {
		slog.Error("reconcile triggers", "err", err)
	}
	a.Checks(ctx, a.Cfg.Now())
	if err := a.Runner.ResumeInterrupted(ctx); err != nil {
		slog.Error("resume interrupted", "err", err)
	}
	a.PumpChats(ctx)
	go a.Loop(ctx, 30*time.Second)

	server := api.New(a, token, web.FS())
	server.Pair(port, port+1)
	if *tailnet != "" {
		node, err := tsnet.Start(ctx, tsnet.Config{Hostname: *tailnet, Dir: deckhome.Layout(home).Tailscale(), Health: a.TailnetHealth}, server)
		if err != nil {
			return fmt.Errorf("tailscale: %w", err)
		}
		defer node.Close()
	}
	srv := &http.Server{Handler: server.Handler()}
	side := &http.Server{Handler: server.Sandbox()}
	go func() { _ = side.Serve(sideLn) }()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = side.Shutdown(sctx)
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("deck listening", "url", fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token), "home", home)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Serve returns once Shutdown starts; the calls still running finish before deck closes.
	<-stopped
	return nil
}

// cleanEnv restarts deck with only the variables it needs: on macOS a confined program reads the
// environment of every process of the owner.
func cleanEnv() error {
	env := os.Environ()
	clean := slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return !slices.Contains(keptEnv, name) && !strings.HasPrefix(name, "LC_") && !strings.HasPrefix(name, "XDG_")
	})
	if len(clean) == len(env) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, clean)
}

var keptEnv = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR", "CODEX_HOME", "DECK_HOME"}

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

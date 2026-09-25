package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider/claudecode"
	"deck2/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:7700", "listen address")
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
	token, err := loadToken(filepath.Join(home, "token"))
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	a, err := app.New(app.Config{
		Home: home, MCPBase: "http://" + ln.Addr().String(),
		QuestionWait: 30 * time.Minute, ConfirmWait: 30 * time.Minute, ConfirmTTL: 24 * time.Hour,
	}, claudecode.New())
	if err != nil {
		return err
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.UpgradeManifests(ctx); err != nil {
		slog.Error("upgrade manifests", "err", err)
	}
	if err := a.Reconcile(ctx); err != nil {
		slog.Error("reconcile triggers", "err", err)
	}
	if err := a.Runner.ResumeInterrupted(ctx); err != nil {
		slog.Error("resume interrupted", "err", err)
	}
	go a.Loop(ctx, 30*time.Second)

	srv := &http.Server{Handler: api.New(a, token, web.FS()).Handler()}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("deck listening", "url", fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token), "home", home)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func loadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if tok := strings.TrimSpace(string(b)); err == nil && len(tok) >= 32 {
		return tok, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	return tok, os.WriteFile(path, []byte(tok+"\n"), 0o600)
}

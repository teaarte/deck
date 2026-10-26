package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Addr is where deck listens; the next port is the second origin of its custom blocks.
const Addr = "127.0.0.1:7700"

// Runner starts deck where it lives and reads its token there: the binary beside the launcher on macOS, the deck of
// its WSL distribution on Windows.
type Runner interface {
	// Command is deck started to listen on addr; it runs until stopped.
	Command(addr string) *exec.Cmd
	// Token is the access token of the owner deck keeps in its home.
	Token(ctx context.Context) (string, error)
	// Stop asks the deck it started to stop; Kill ends it when it does not.
	Stop(cmd *exec.Cmd) error
	Kill(cmd *exec.Cmd)
}

// Deck is the deck the launcher opens: one that already answers, or one it starts and stops on quit.
type Deck struct {
	Runner Runner
	Addr   string
	mu     sync.Mutex
	cmd    *exec.Cmd
	// exited closes when the deck the launcher started ends.
	exited chan struct{}
}

// Up says whether deck answers at its address and has started: its page, and its API asking for the token, so the
// token goes to no other program on that port.
func (d *Deck) Up(ctx context.Context) bool {
	page, ok := d.get(ctx, "/")
	if !ok || page.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(page.Body, 1<<16))
	api, ok := d.get(ctx, "/api/events")
	return ok && strings.Contains(string(body), "<title>deck</title>") && api.StatusCode == http.StatusUnauthorized &&
		api.Header.Get("WWW-Authenticate") == "Bearer"
}

func (d *Deck) get(ctx context.Context, path string) (*http.Response, bool) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+d.Addr+path, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	res.Body = io.NopCloser(strings.NewReader(string(b)))
	return res, true
}

// Start starts deck unless one answers already, and waits until it has started or ctx ends; a second call while
// one starts waits for the same deck.
func (d *Deck) Start(ctx context.Context) error {
	if d.Up(ctx) {
		return nil
	}
	d.mu.Lock()
	if d.cmd == nil {
		cmd := d.Runner.Command(d.Addr)
		if err := cmd.Start(); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("start deck: %w", err)
		}
		exited := make(chan struct{})
		d.cmd, d.exited = cmd, exited
		go func() {
			_ = cmd.Wait()
			close(exited)
			d.mu.Lock()
			if d.cmd == cmd {
				d.cmd = nil
			}
			d.mu.Unlock()
		}()
	}
	exited := d.exited
	d.mu.Unlock()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for !d.Up(ctx) {
		select {
		case <-exited:
			if d.Up(ctx) {
				return nil
			}
			return errors.New("deck stopped as it started")
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

// URL is the address of deck with the owner's token, which signs the window in; a deck that does not take the token
// is another deck on that port.
func (d *Deck) URL(ctx context.Context) (string, error) {
	token, err := d.Runner.Token(ctx)
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("deck has no token yet")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+d.Addr+"/api/commands/deck.menu", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("another deck answers at %s: it does not take the owner's token", d.Addr)
	}
	return "http://" + d.Addr + "/?token=" + url.QueryEscape(token), nil
}

// Stop stops the deck the launcher started, if it did, and ends it when it is not gone in a while.
func (d *Deck) Stop() error {
	d.mu.Lock()
	cmd, exited := d.cmd, d.exited
	d.mu.Unlock()
	if cmd == nil {
		return nil
	}
	err := d.Runner.Stop(cmd)
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		d.Runner.Kill(cmd)
		<-exited
	}
	return err
}

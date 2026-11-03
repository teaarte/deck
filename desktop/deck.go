package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Addr is where deck listens; the next port is the second origin of its custom blocks.
const Addr = "127.0.0.1:7700"

// Runner starts deck where it lives and runs deck pair there: the binary beside the launcher on macOS, the deck of
// its WSL distribution on Windows.
type Runner interface {
	// Command is deck started to listen on addr; it runs until stopped.
	Command(addr string) *exec.Cmd
	// Pair is what deck pair --device launcher prints: the link with a code that pairs the launcher's window.
	Pair(ctx context.Context) (string, error)
	// Version is the version of the deck it starts, as deck -version says it.
	Version(ctx context.Context) (string, error)
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
	// Update runs when the deck the launcher started ends asking for an update; updating keeps deck down meanwhile.
	Update   func()
	updating bool
	// key signs the window in; the launcher alone holds it besides the page, read and set by one reveal at a time.
	key string
}

// Up says whether deck answers at its address and has started: its page, and its API asking to sign in.
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
	if d.updating {
		d.mu.Unlock()
		return errors.New("deck is updating")
	}
	if d.cmd == nil {
		cmd := d.Runner.Command(d.Addr)
		if err := cmd.Start(); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("start deck: %w", err)
		}
		exited := make(chan struct{})
		d.cmd, d.exited = cmd, exited
		go func() {
			err := cmd.Wait()
			var exit *exec.ExitError
			update := errors.As(err, &exit) && exit.ExitCode() == UpdateCode && d.Update != nil
			d.mu.Lock()
			if d.cmd == cmd {
				d.cmd = nil
			}
			d.updating = d.updating || update
			d.mu.Unlock()
			close(exited)
			if update {
				go func() {
					d.Update()
					d.mu.Lock()
					d.updating = false
					d.mu.Unlock()
				}()
			}
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

// Signed says the key of the window still signs it in, so the window stays as it is.
func (d *Deck) Signed(ctx context.Context) bool {
	if d.key == "" {
		return false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+d.Addr+"/api/commands/deck.menu_get", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+d.key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Pair pairs the window by the code deck pair wrote and gives the address it opens with its key in the fragment; a
// deck that does not take the code is another deck on that port, of another home.
func (d *Deck) Pair(ctx context.Context) (string, error) {
	out, err := d.Runner.Pair(ctx)
	_, code, ok := strings.Cut(strings.TrimSpace(out), "#pair=")
	if err != nil || !ok {
		return "", fmt.Errorf("deck pair: %s %v", strings.TrimSpace(out), err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+d.Addr+"/auth/pair/begin", bytes.NewReader(body))
	req.Header.Set("Origin", "http://"+d.Addr)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var b struct{ Key string }
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&b) != nil || b.Key == "" {
		return "", fmt.Errorf("another deck answers at %s: it does not take the code of deck pair", d.Addr)
	}
	d.key = b.Key
	return "http://" + d.Addr + "/#key=" + b.Key, nil
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

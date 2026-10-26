package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
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
	// Stop asks the deck it started to stop.
	Stop(cmd *exec.Cmd) error
}

// Deck is the deck the launcher opens: one that already answers, or one it starts and stops on quit.
type Deck struct {
	Runner Runner
	Addr   string
	cmd    *exec.Cmd
	exited chan error
}

// Up says whether deck answers and has started.
func (d *Deck) Up(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+d.Addr+"/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Start starts deck unless one answers already, and waits until it has started or ctx ends.
func (d *Deck) Start(ctx context.Context) error {
	if d.Up(ctx) {
		return nil
	}
	if d.cmd == nil {
		d.cmd = d.Runner.Command(d.Addr)
		if err := d.cmd.Start(); err != nil {
			d.cmd = nil
			return fmt.Errorf("start deck: %w", err)
		}
		d.exited = make(chan error, 1)
		go func(cmd *exec.Cmd, exited chan error) { exited <- cmd.Wait() }(d.cmd, d.exited)
	}
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for !d.Up(ctx) {
		select {
		case err := <-d.exited:
			d.cmd = nil
			return fmt.Errorf("deck stopped as it started: %v", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

// URL is the address of deck with the owner's token, which signs the window in.
func (d *Deck) URL(ctx context.Context) (string, error) {
	token, err := d.Runner.Token(ctx)
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("deck has no token yet")
	}
	return "http://" + d.Addr + "/?token=" + url.QueryEscape(token), nil
}

// Stop stops the deck the launcher started, if it did, and waits for it a while.
func (d *Deck) Stop() error {
	if d.cmd == nil {
		return nil
	}
	err := d.Runner.Stop(d.cmd)
	select {
	case <-d.exited:
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Kill()
	}
	d.cmd = nil
	return err
}

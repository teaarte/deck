package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// built is deck built from this repository for the tests.
func built(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "deck")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/deck")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	return bin
}

// freeAddr is a loopback address whose port and the next one are free.
func freeAddr(t *testing.T) string {
	t.Helper()
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().(*net.TCPAddr)
		ln.Close()
		if next, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port+1))); err == nil {
			next.Close()
			return addr.String()
		}
	}
	t.Fatal("no free pair of ports")
	return ""
}

// opens checks that deck, started, lets the window in at its address with the token, and stops with the launcher.
func opens(t *testing.T, d *Deck) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("deck starts: %v", err)
	}
	u, err := d.URL(ctx)
	if err != nil || !strings.HasPrefix(u, "http://"+d.Addr+"/?token=") {
		t.Fatalf("the address of the window carries the token: %q %v", u, err)
	}
	token := u[strings.Index(u, "=")+1:]
	req, _ := http.NewRequest(http.MethodPost, "http://"+d.Addr+"/api/commands/deck.menu", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("the token signs the owner in: %v %v", res, err)
	}
	res.Body.Close()
	if err := (&Deck{Runner: d.Runner, Addr: d.Addr}).Start(ctx); err != nil {
		t.Fatalf("a second launcher finds deck up and starts none: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("deck stops on quit: %v", err)
	}
	if d.Up(ctx) {
		t.Fatal("deck is down after quit")
	}
}

// TestTheLauncherStartsDeckAndSignsTheWindowIn: on macOS the deck beside the launcher starts in its home, the window
// gets its address with the owner's token, and quitting the launcher stops it.
func TestTheLauncherStartsDeckAndSignsTheWindowIn(t *testing.T) {
	opens(t, &Deck{Runner: Local{Bin: built(t), Home: t.TempDir()}, Addr: freeAddr(t)})
}

// TestTheLauncherRunsTheDeckOfWSL: on Windows the same goes through wsl.exe into the distribution of deck; here a
// wsl.exe that runs the command on this machine stands for it.
func TestTheLauncherRunsTheDeckOfWSL(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "wsl.exe")
	script := `#!/bin/sh
while [ "$1" != "--exec" ]; do shift; done; shift
export HOME="` + home + `"
case "$1" in
` + Bin + `) shift; echo $$ > "` + dir + `/pid"; exec "` + built(t) + `" "$@" ;;
pkill) exec kill -TERM "$(cat "` + dir + `/pid")" ;;
*) exec "$@" ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	opens(t, &Deck{Runner: WSL{Exe: fake, Distro: "deck"}, Addr: freeAddr(t)})
}

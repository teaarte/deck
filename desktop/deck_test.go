package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// opens checks that deck, started, pairs the window by deck pair with a key in the fragment of its address, keeps it
// while it signs in, and stops with the launcher.
func opens(t *testing.T, d *Deck) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("deck starts: %v", err)
	}
	if d.Signed(ctx) {
		t.Fatal("a window not paired yet is not signed in")
	}
	u, err := d.Pair(ctx)
	if err != nil || !strings.HasPrefix(u, "http://"+d.Addr+"/#key=") {
		t.Fatalf("the address of the window carries its key in the fragment: %q %v", u, err)
	}
	key := u[strings.Index(u, "=")+1:]
	req, _ := http.NewRequest(http.MethodPost, "http://"+d.Addr+"/api/commands/deck.devices_list", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("the key signs the window in: %v %v", res, err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), `"kind":"launcher"`) || !d.Signed(ctx) {
		t.Fatalf("the window is the device of the launcher and stays signed in: %s", b)
	}
	if again, err := d.Pair(ctx); err != nil || again == u {
		t.Fatalf("a window paired again gets a new key: %v", err)
	}
	req, _ = http.NewRequest(http.MethodPost, "http://"+d.Addr+"/api/commands/deck.menu_get", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+key)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the key before it signs nothing in: %v %v", res, err)
	}
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
// gets its address with the key deck pair gave, and quitting the launcher stops it.
func TestTheLauncherStartsDeckAndSignsTheWindowIn(t *testing.T) {
	opens(t, &Deck{Runner: Local{Bin: built(t), Home: t.TempDir()}, Addr: freeAddr(t)})
}

// TestTheLauncherRunsTheDeckOfWSL: on Windows the same goes through wsl.exe into the distribution of deck, deck under
// the login shell of its user; here a wsl.exe that runs the command on this machine stands for it.
func TestTheLauncherRunsTheDeckOfWSL(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "[ -n \"$WSL_FAKE_LOGIN\" ] && echo login > \"$HOME/login\"\nunset WSL_FAKE_LOGIN\n"
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "wsl.exe")
	script := `#!/bin/sh
while [ "$1" != "--exec" ]; do shift; done; shift
export HOME="` + home + `"
case "$1" in
bash) for a; do shift; [ "$a" = ` + Bin + ` ] && a="` + built(t) + `"; set -- "$@" "$a"; done
  echo $$ > "` + dir + `/pid"; WSL_FAKE_LOGIN=1 exec "$@" ;;
pkill) exec kill -TERM "$(cat "` + dir + `/pid")" ;;
` + Bin + `) shift; exec "` + built(t) + `" "$@" ;;
*) exec "$@" ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	opens(t, &Deck{Runner: WSL{Exe: fake, Distro: "deck"}, Addr: freeAddr(t)})
	if _, err := os.Stat(filepath.Join(home, "login")); err != nil {
		t.Fatalf("deck starts under the login shell of its user, whose profile sets its PATH: %v", err)
	}
}

// TestTheLauncherKeepsThePathOfWindowsOutOfWSL: preparing the distribution sets in wsl.conf the user deck and no
// PATH of Windows, whose npm and node would stand for those of Linux, keeps the rest and gives the same file again.
func TestTheLauncherKeepsThePathOfWindowsOutOfWSL(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "wsl.conf")
	if err := os.WriteFile(conf, []byte("[boot]\nsystemd=true\n[interop]\nappendWindowsPath = true\nenabled=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "wsl.exe")
	script := `#!/bin/sh
case "$*" in
*--status*|*--terminate*) exit 0 ;;
*"-l -q"*) echo deck; exit 0 ;;
esac
while [ "$1" != "--exec" ]; do shift; done; shift
[ "$4" = /etc/wsl.conf ] && exec sh -c "$3" sh "` + conf + `"
echo "$3" > "` + dir + `/provision"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w := WSL{Exe: fake, Distro: "deck"}
	var once []byte
	for i := range 2 {
		if err := w.Setup("deck-linux", func(string) {}); err != nil {
			t.Fatalf("setup: %v", err)
		}
		got, _ := os.ReadFile(conf)
		if i == 1 && string(got) != string(once) {
			t.Fatalf("a second setup gives the same wsl.conf:\n%s\nthen\n%s", once, got)
		}
		once = got
	}
	want := "[boot]\nsystemd=true\n[interop]\nappendWindowsPath=false\nenabled=true\n\n[user]\ndefault=deck\n"
	if string(once) != want {
		t.Fatalf("wsl.conf names the user deck and leaves the PATH of Windows out, the rest kept:\n%s", once)
	}
	if p, _ := os.ReadFile(filepath.Join(dir, "provision")); !strings.Contains(string(p), "useradd") {
		t.Fatalf("the distribution is provisioned: %q", p)
	}
}

// TestTheLauncherPairsOnlyWithItsDeck: another program on deck's port is not taken for deck, and a deck of another
// home that does not take the code of deck pair is said to be another.
func TestTheLauncherPairsOnlyWithItsDeck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte("<title>deck</title>"))
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	d := &Deck{Runner: Local{Bin: built(t), Home: t.TempDir()}, Addr: strings.TrimPrefix(srv.URL, "http://")}
	ctx := context.Background()
	if !d.Up(ctx) {
		t.Fatal("a server that answers as deck is up")
	}
	if _, err := d.Pair(ctx); err == nil || !strings.Contains(err.Error(), "another deck") {
		t.Fatalf("a deck that does not take the code is another: %v", err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<title>deck</title>")) }))
	defer other.Close()
	if (&Deck{Addr: strings.TrimPrefix(other.URL, "http://")}).Up(ctx) {
		t.Fatal("a page alone, without deck's API, is not deck")
	}
}

package owner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

type deckBinary struct {
	cmd         *exec.Cmd
	url, cookie string
	said        func() []string
}

var (
	listening = regexp.MustCompile(`url=(http://[^ ]+)`)
	firstLink = regexp.MustCompile(`link="?(http://[^/]+)/#pair=([A-Za-z0-9_-]+)`)
)

// startDeck builds deck, starts it on home with env added and waits until it listens.
func startDeck(t *testing.T, home string, env ...string) *deckBinary {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "deck")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(harness.Root, "cmd/deck")).CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	// Agent CLIs that fail stand before the owner's, so a self-check never runs on the owner's accounts.
	shims := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := harness.WriteProgram(filepath.Join(shims, name), []byte("#!/bin/sh\nexit 1\n")); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "-addr", "127.0.0.1:0")
	cmd.Env = append(append(os.Environ(), "DECK_HOME="+home, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH")), env...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	up, link := make(chan []string, 1), make(chan []string, 1)
	var mu sync.Mutex
	var said []string
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			mu.Lock()
			said = append(said, sc.Text())
			mu.Unlock()
			if m := listening.FindStringSubmatch(sc.Text()); m != nil {
				up <- m
			}
			if m := firstLink.FindStringSubmatch(sc.Text()); m != nil {
				link <- m
			}
		}
	}()
	started := false
	defer func() {
		if !started {
			mu.Lock()
			defer mu.Unlock()
			t.Logf("deck said:\n%s", strings.Join(said, "\n"))
		}
	}()
	m := harness.Receive(t, "deck to start", up)
	l := harness.Receive(t, "the link of the first device in the log", link)
	started = true
	cookie, err := harness.NewPasskey().Pair(m[1], l[1], l[2], "first")
	if err != nil {
		t.Fatal(err)
	}
	return &deckBinary{cmd: cmd, url: m[1], cookie: cookie, said: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(said)
	}}
}

func (d *deckBinary) call(name string, in any) (int, string, error) {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", d.url+"/api/commands/"+name, bytes.NewReader(b))
	req.AddCookie(&http.Cookie{Name: "deck", Value: d.cookie})
	req.Header.Set("Origin", d.url)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String(), nil
}

func TestDeckRestartsItselfWithACleanEnvironment(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	d := startDeck(t, home, "DECK_TEST_KEY=sk-marker-7f3a", "NODE_OPTIONS=--require=/tmp/x.js", "LC_ALL=C")
	env := startEnv(t, d.cmd.Process.Pid)
	if !slices.Contains(env, "DECK_HOME="+home) || !slices.Contains(env, "LC_ALL=C") {
		t.Fatalf("deck keeps DECK_HOME and the locale: %q", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "DECK_TEST_KEY=") || strings.HasPrefix(kv, "NODE_OPTIONS=") {
			t.Fatalf("another process of the owner reads %s in the environment of deck", kv)
		}
	}
}

func TestAStopOfDeckLetsACallInFlightFinish(t *testing.T) {
	t.Parallel()
	slow := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		time.Sleep(2 * time.Second)
		harness.JsonReply(w, `{"ok": true}`)
	})
	d := startDeck(t, t.TempDir())
	// Deck reaches a service on its own machine only when the domains name its address.
	m := harness.Manifest(fmt.Sprintf(`{"domains": {"read": [%q]}, "tools": {"http": [{"name": "slow", "description": "Answers slowly.", "method": "GET",
  "url": %q, "class": "internal", "input": {"type": "object", "properties": {}}}]}}`, strings.TrimPrefix(slow.URL, "http://"), slow.URL+"/slow"))
	if code, out, err := d.call("deck.project_create", map[string]any{"name": "late", "manifest": m}); err != nil || code != 200 {
		t.Fatalf("project: %d %s %v", code, out, err)
	}
	type answer struct {
		code int
		out  string
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		code, out, err := d.call("late.slow", map[string]any{})
		got <- answer{code, out, err}
	}()
	harness.Until(t, "the call to reach the tool", func() bool { return len(slow.Requests()) > 0 })
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	a := <-got
	if a.err != nil || a.code != 200 || !strings.Contains(a.out, "ok") {
		t.Fatalf("a call already out finishes and records its outcome before deck exits: %+v", a)
	}
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	harness.Receive(t, "deck to exit after the stop", done)
}

func TestTheHomeOfDeckIsOnlyTheOwnersAndItsLogHasNoToken(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(deckhome.Layout(home).State(), 0o755); err != nil {
		t.Fatal(err)
	}
	d := startDeck(t, home)
	if code, out, err := d.call("deck.menu_get", map[string]any{}); err != nil || code != 200 {
		t.Fatalf("the first device pairs by the link in the log and calls: %d %s %v", code, out, err)
	}
	if fi, err := os.Stat(home); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the home is only the owner's: %v %v", fi, err)
	}
	for _, line := range d.said() {
		if strings.Contains(line, "token") {
			t.Fatalf("the log of deck carries no token: %s", line)
		}
	}
	if _, err := os.Stat(filepath.Join(deckhome.Layout(home).State(), "token")); err == nil {
		t.Fatal("deck keeps no token of the owner")
	}
}

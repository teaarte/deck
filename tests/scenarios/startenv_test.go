package scenarios

import (
	"bufio"
	"bytes"
	deckhome "deck2/internal/home"
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
)

type deckBinary struct {
	cmd        *exec.Cmd
	url, token string
}

var listening = regexp.MustCompile(`url="(http://[^/]+)/\?token=([0-9a-f]+)"`)

// startDeck builds deck, starts it on home with env added and waits until it listens.
func startDeck(t *testing.T, home string, env ...string) *deckBinary {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "deck")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(Root, "cmd/deck")).CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	// Agent CLIs that fail stand before the owner's, so a self-check never runs on the owner's accounts.
	shims := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(shims, name), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
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
	up := make(chan []string, 1)
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
	m := Receive(t, "deck to start", up)
	started = true
	return &deckBinary{cmd: cmd, url: m[1], token: m[2]}
}

func (d *deckBinary) call(name string, in any) (int, string, error) {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", d.url+"/api/commands/"+name, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+d.token)
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
	slow := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		time.Sleep(2 * time.Second)
		jsonReply(w, `{"ok": true}`)
	})
	d := startDeck(t, t.TempDir())
	m := fmt.Sprintf(`{"version": 13, "tools": {"http": [{"name": "slow", "description": "Answers slowly.", "method": "GET",
  "url": %q, "class": "internal", "input": {"type": "object", "properties": {}}}]}}`, slow.URL+"/slow")
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
	Until(t, "the call to reach the tool", func() bool { return len(slow.requests()) > 0 })
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	a := <-got
	if a.err != nil || a.code != 200 || !strings.Contains(a.out, "ok") {
		t.Fatalf("a call already out finishes and records its outcome before deck exits: %+v", a)
	}
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	Receive(t, "deck to exit after the stop", done)
}

func TestTheHomeAndTheTokenOfDeckAreOnlyTheOwners(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(deckhome.Layout(home).State(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deckhome.Layout(home).State(), "token"), []byte(strings.Repeat("ab", 24)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(filepath.Join(deckhome.Layout(home).State(), "token"), 0o644)
	d := startDeck(t, home)
	if d.token != strings.Repeat("ab", 24) {
		t.Fatalf("deck keeps the token it has: %s", d.token)
	}
	for path, want := range map[string]os.FileMode{home: 0o700, filepath.Join(deckhome.Layout(home).State(), "token"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s is only the owner's: %v %v", path, fi.Mode().Perm(), err)
		}
	}
}

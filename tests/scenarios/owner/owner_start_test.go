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
	cmd      *exec.Cmd
	bin      string
	home     string
	url, key string
	said     func() []string
}

var (
	listening = regexp.MustCompile(`url=(http://[^ ]+)`)
	firstLink = regexp.MustCompile(`link="?(https://[^/]+)/#pair=([A-Za-z0-9_-]+)`)
)

// buildDeck builds the binary of deck for a test.
func buildDeck(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "deck")
	if out, err := exec.Command("go", "build", "-o", bin, filepath.Join(harness.Root, "cmd/deck")).CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	return bin
}

// startDeck builds deck, starts it on home with env added, waits until it listens and pairs a launcher by deck pair.
func startDeck(t *testing.T, home string, env ...string) *deckBinary {
	t.Helper()
	return runDeck(t, buildDeck(t), home, nil, env...)
}

// runDeck starts the binary of deck on home with its args and env added, waits until it listens and pairs a launcher
// by deck pair, as the launcher does.
func runDeck(t *testing.T, bin, home string, args []string, env ...string) *deckBinary {
	t.Helper()
	// Agent CLIs that fail stand before the owner's, so a self-check never runs on the owner's accounts.
	shims := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := harness.WriteProgram(filepath.Join(shims, name), []byte("#!/bin/sh\nexit 1\n")); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, append([]string{"-addr", "127.0.0.1:0"}, args...)...)
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
	d := &deckBinary{cmd: cmd, bin: bin, home: home, said: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(said)
	}}
	started := false
	defer func() {
		if !started {
			t.Logf("deck said:\n%s", strings.Join(d.said(), "\n"))
		}
	}()
	m := harness.Receive(t, "deck to start", up)
	d.url = m[1]
	if d.key, err = d.launcher(); err != nil {
		t.Fatal(err)
	}
	started = true
	return d
}

// pair runs deck pair on the home of d and gives what it printed.
func (d *deckBinary) pair(args ...string) (string, error) {
	cmd := exec.Command(d.bin, append([]string{"pair"}, args...)...)
	cmd.Env = append(os.Environ(), "DECK_HOME="+d.home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// launcher pairs a launcher by deck pair --device launcher and gives its key.
func (d *deckBinary) launcher() (string, error) {
	out, err := d.pair("--device", "launcher")
	link := strings.TrimSpace(out)
	if err != nil || !strings.Contains(link, "/#pair=") {
		return "", fmt.Errorf("deck pair --device launcher: %s %v", out, err)
	}
	base, code, _ := strings.Cut(link, "/#pair=")
	status, body, _, err := harness.Rite(base, base, "pair/begin", map[string]any{"code": code})
	var b struct{ Key string }
	if err != nil || status != 200 || json.Unmarshal(body, &b) != nil || b.Key == "" {
		return "", fmt.Errorf("the launcher pairs at %s: %d %s %v", base, status, body, err)
	}
	return b.Key, nil
}

func (d *deckBinary) call(name string, in any) (int, string, error) {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", d.url+"/api/commands/"+name, bytes.NewReader(b))
	harness.Bearer(d.key)(req)
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
		t.Fatalf("the launcher pairs by deck pair and calls: %d %s %v", code, out, err)
	}
	if !slices.ContainsFunc(d.said(), func(l string) bool { return strings.Contains(l, "no device is paired yet") }) {
		t.Fatalf("deck with no https address says how a device pairs: %q", d.said())
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

// TestDeckPairPairsWhetherDeckRunsOrNot: deck pair writes a code deck takes while it runs and after a start; it prints
// the links of deck's https addresses it kept, the first start logs one, and a used code pairs no second device.
func TestDeckPairPairsWhetherDeckRunsOrNot(t *testing.T) {
	t.Parallel()
	home, bin := t.TempDir(), buildDeck(t)
	public := "https://deck.example.test"
	d := runDeck(t, bin, home, []string{"-public", public})
	harness.Until(t, "the link of the first device in the log", func() bool {
		return slices.ContainsFunc(d.said(), firstLink.MatchString)
	})
	out, err := d.pair("--name", "phone")
	if err != nil || !strings.Contains(out, public+"/#pair=") {
		t.Fatalf("deck pair prints the link of the https address while deck runs: %s %v", out, err)
	}
	if _, err := harness.NewPasskey().Pair(d.url, public, harness.Code(strings.TrimSpace(out[strings.Index(out, public):])), ""); err != nil {
		t.Fatal(err)
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	_ = d.cmd.Wait()
	stopped, err := d.pair()
	if err != nil || !strings.Contains(stopped, public+"/#pair=") {
		t.Fatalf("deck pair prints a link while deck is stopped: %s %v", stopped, err)
	}
	code := harness.Code(strings.TrimSpace(stopped[strings.Index(stopped, public):]))
	d2 := runDeck(t, bin, home, []string{"-public", public})
	if _, err := harness.NewPasskey().Pair(d2.url, public, code, "laptop"); err != nil {
		t.Fatalf("a code deck pair wrote while deck was stopped pairs after its start: %v", err)
	}
	if _, err := harness.NewPasskey().Pair(d2.url, public, code, "again"); err == nil {
		t.Fatal("a code pairs one device only")
	}
	if code, out, err := d2.call("deck.devices_list", map[string]any{}); err != nil || code != 200 ||
		!strings.Contains(out, `"laptop"`) || !strings.Contains(out, `"phone"`) || strings.Count(out, `"kind":"launcher"`) != 1 {
		t.Fatalf("the devices are the two browsers and the last launcher: %d %s %v", code, out, err)
	}
}

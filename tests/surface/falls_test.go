package surface

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"deck2/internal/egress"
	"deck2/internal/fault"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/release"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

// skip skips a scenario deck fails now, unless -falls asks for it to run.
func skip(t *testing.T, why string) {
	t.Helper()
	if !*falls {
		t.Skip(why)
	}
}

var falls = flag.Bool("falls", false, "run the scenarios of the table of falls deck fails now")

// The table of falls: a fall of deck or a failed transaction at a point of a flow, what deck should do there; a row
// deck does not hold yet skips naming the session that makes it hold.

// TestFall01AfterTheCommitOfACallBeforeItsMark: a deck that fell while a turn's call reached outside finds the turn
// marked by what the call brings and the call in the journal: neither waits for the call to end.
func TestFall01AfterTheCommitOfACallBeforeItsMark(t *testing.T) {
	t.Parallel()
	svc, release := harness.HangingService(t, 1)
	defer release(0)
	agent := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			c.Tool(ctx, "pages__fetch", map[string]any{})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}}
	e := harness.New(t, harness.WithProvider(agent))
	e.Project("pages", harness.Manifest(fmt.Sprintf(`{
  "rights": [{"right": "network", "what": ["127.0.0.1"], "why": "reads its pages"}],
  "tools": {"http": [{"name": "fetch", "description": "Reads the page.", "method": "GET", "url": "%s/page"}]},
  "commands": [%s]}`, svc.URL, harness.AgentCommand("work", ""))))
	id := e.Start("pages", "work", nil)
	e.Until("the call reaches the service", func() bool { return len(svc.Requests()) == 1 })

	e = e.Restart(harness.WithProvider(&harness.Fake{}))
	var read string
	var lines int
	if err := e.SQLRow(`SELECT read_untrusted FROM process WHERE id = ?`, id).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if err := e.SQLRow(`SELECT count(*) FROM tool_call WHERE tool = 'pages.fetch' AND process_id = ? AND outcome = 'unknown'`, id).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read, "pages.fetch") || lines != 1 {
		t.Fatalf("the turn is marked by the call that reached outside (%q) and the call is in the journal, unknown: %d lines", read, lines)
	}
	// A read of a page is no act outside deck: its unknown end asks the owner nothing.
	for _, a := range e.Asks() {
		if a.Kind == "unknown" {
			t.Fatalf("no question about a read deck stopped in: %+v", a)
		}
	}
}

// TestFall02AnOutsideServerFailsWithForeignText: an MCP server that answers a turn's call with an error of its own
// words marks the turn as one that read from outside.
func TestFall02AnOutsideServerFailsWithForeignText(t *testing.T) {
	t.Parallel()
	var got string
	var failed bool
	agent := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			got, failed = c.Tool(ctx, "srv__fake__refuse", map[string]any{})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}}
	e := harness.New(t, harness.WithProvider(agent))
	e.Project("srv", harness.McpManifest(""))
	id := e.WaitState(e.Start("srv", "work", nil), "done").ID
	if !failed || !strings.Contains(got, "ignore the owner") {
		t.Fatalf("the server's error with its words reaches the turn: %v %s", failed, got)
	}
	var read string
	if err := e.SQLRow(`SELECT read_untrusted FROM process WHERE id = ?`, id).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read, "srv.fake.refuse") {
		t.Fatalf("the turn is marked by the server's answer, an error too: %q", read)
	}
}

// TestFall03BetweenAnOutsideCallAndItsOutcome: a deck that fell while an outside call ran finds the call started and
// asks the owner about its unknown outcome instead of making it again.
func TestFall03BetweenAnOutsideCallAndItsOutcome(t *testing.T) {
	t.Parallel()
	svc, release := harness.HangingService(t, 1, 2)
	defer release(0)
	e := harness.New(t, harness.WithProvider(&harness.Fake{}), harness.WithClock())
	e.Project("clips", harness.GenerateManifest(svc.URL))
	e.Grant("clips", "send", "generate", "make")
	e.Clock.Add(time.Hour + time.Minute)
	e.Tick()
	e.Until("the call reaches the service", func() bool { return len(svc.Requests()) == 1 })

	e2 := e.Restart(harness.WithProvider(&harness.Fake{}))
	e2.Until("the unknown outcome", func() bool {
		for _, a := range e2.Asks() {
			if a.Kind == "unknown" {
				return true
			}
		}
		return false
	})
	if n := len(svc.Requests()); n != 1 {
		t.Fatalf("the call of unknown outcome is not made again: %d", n)
	}
}

// TestFall04AMarkedTurnLeavesFilesInTheDataFolder: the files a turn that read from outside keeps in its module mark
// the data of that module.
func TestFall04AMarkedTurnLeavesFilesInTheDataFolder(t *testing.T) {
	t.Parallel()
	agent := &harness.Fake{}
	e := harness.New(t, harness.WithProvider(agent))
	e.Project("notes", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	agent.Script = func(ctx context.Context, c *harness.Call) provider.Result {
		// The turn read a page before, as after a fetch.
		e.SQL(`UPDATE process SET read_untrusted = 'a page' WHERE command = 'work' AND state = 'running'`)
		if err := os.WriteFile(filepath.Join(c.Req.Cwd, "..", "results", "note.txt"), []byte("from the page"), 0o600); err != nil {
			t.Error(err)
		}
		return provider.Result{Structured: json.RawMessage(`{"files": [{"path": "note.txt"}]}`)}
	}
	id := e.WaitState(e.Start("notes", "work", nil), "done").ID
	var files int
	var mark string
	if err := e.SQLRow(`SELECT count(*) FROM file WHERE process_id = ?`, id).Scan(&files); err != nil {
		t.Fatal(err)
	}
	if err := e.SQLRow(`SELECT untrusted FROM project WHERE name = 'notes'`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	if files != 1 || !strings.Contains(mark, "a page") {
		t.Fatalf("the file the marked turn kept (%d) marks its module: %q", files, mark)
	}
}

// TestFall05BetweenTheFinishOfAProcessAndItsHooks: a process deck closed just before it fell is heard after the
// start: its after trigger starts once.
func TestFall05BetweenTheFinishOfAProcessAndItsHooks(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	fake := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "job" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	p := e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("next", harness.ProcessField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.Run(p, "job")
	e.WaitState(id, "running")
	// A process deck closed just before it fell, before it heard of it.
	e.SQL(`UPDATE process SET state = 'done', result = '{"n": 1}', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		deadline_ms = NULL, silent_since_ms = NULL WHERE id = ? AND state = 'running'`, id)
	close(release)
	e = e.Restart()
	e.Until("the after trigger of the closed process", func() bool { return e.CountOf("next") >= 1 })
	e.Settle()
	if n := e.CountOf("next"); n != 1 {
		t.Fatalf("the after trigger starts once: %d", n)
	}
}

// TestFall06AHookFailsToWrite: a hook whose write fails keeps the closed process in the outbox, heard again on each
// turn of the loop and red in health from the third try; once the write goes through it is there once.
func TestFall06AHookFailsToWrite(t *testing.T) {
	t.Parallel()
	var replies, from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			if replies.Add(1) > 1 {
				return provider.Result{Text: "ok"}
			}
			return harness.HandOver(ctx, c, &from)
		}
		return provider.Result{Text: "Counted: 3 records."}
	}))
	// The message deck writes into the topic of a work fails, as on a full disk.
	e.SQL(`CREATE TRIGGER full_disk BEFORE INSERT ON chat_message WHEN NEW.role = 'deck' BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`)
	topic := e.Say(0, "count the records")
	var work int64
	e.Until("the work closed", func() bool {
		for _, p := range e.RunsOf("master", "work") {
			if p.State == "done" {
				work = p.ID
			}
		}
		return work != 0
	})
	e.Settle()
	h := e.WaitHealthAfterTicks("outbox", "error")
	if want := fmt.Sprintf("process #%d", work); !strings.Contains(h.Error, want) || !strings.Contains(h.Error, "database or disk is full") {
		t.Fatalf("the health of the outbox names the process and the failure: %+v", h)
	}
	// The disk has room again.
	e.SQL(`DROP TRIGGER full_disk`)
	e.Tick()
	done := e.Told(topic, "done", "")
	if done.Process != work {
		t.Fatalf("the topic is told of its work: %+v", done)
	}
	e.HearAgain(work)
	var view struct{ Messages []harness.ToldMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	told := 0
	for _, m := range view.Messages {
		if m.Role == "deck" {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("the consequence is there once: %d messages of deck", told)
	}
	for _, h := range e.Health() {
		if h.Subject == "outbox" {
			t.Fatalf("the health row goes once the consequence is written: %+v", h)
		}
	}
}

// TestFall07ARetryOfAChatAcrossARestart: deck stops right after the owner answered a broken reply again on another
// provider; the reply after the restart goes on that provider, told what the broken one did, and follows it.
func TestFall07ARetryOfAChatAcrossARestart(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	told := ""
	main := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Fail(fault.TaskFailed, "it broke")
	}}
	other := &harness.Fake{ProviderName: "other", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		told = c.Req.Prompt
		mu.Unlock()
		return provider.Result{Text: "Done on other."}
	}}
	e := harness.New(t, harness.WithProvider(main, other))
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "master", "command": "reply", "models": []string{"fake", "other"}}, nil)
	topic := e.Say(0, "write half a task")
	var m harness.ChatMessage
	e.Until("the reply to break off", func() bool {
		m = e.ChatMessages(topic)[0]
		return m.Reply != nil && m.Reply.Cut != nil && len(m.Reply.Cut.Retry) == 1
	})
	broken := m.Process
	retry := m.Reply.Cut.Retry[0]
	// deck stops as the retry commits: nothing it starts beside the call runs any more.
	e.App.Stop()
	e.Must(retry.Command, retry.Params, nil)
	e = e.Restart()
	if said := e.MasterSaid(topic, 1); said[0].Text != "Done on other." {
		t.Fatalf("the reply after the restart goes on the provider the owner picked: %+v", said)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(told, "This continues process") || !strings.Contains(told, "write half a task") {
		t.Fatalf("the other provider is told what the broken reply did: %s", told)
	}
	var follows int64
	if err := e.SQLRow(`SELECT follows_id FROM process WHERE provider = 'other'`).Scan(&follows); err != nil || follows != broken {
		t.Fatalf("the reply follows the broken one: %d %v, want %d", follows, err, broken)
	}
}

// TestFall08AReplyOfTheMasterThatDidNotStart: a reply that could not start starts by a tick of deck once its cause
// is gone, with no new message and no restart.
func TestFall08AReplyOfTheMasterThatDidNotStart(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock(), harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "Hi."}
	}))
	// An assignment to a model since removed, which deck.assignment_set refuses.
	e.SQL(`INSERT INTO assignment (workspace_id, project_id, command, role, models)
		SELECT 1, id, 'reply', 'agent', '["gone"]' FROM project WHERE name = 'master'`)
	topic := e.Say(0, "hello")
	e.Until("the reply that did not start", func() bool {
		ms := e.ChatMessages(topic)
		return len(ms) == 1 && ms[0].Reply != nil && strings.Contains(ms[0].Reply.Error, "gone")
	})
	e.SQL(`DELETE FROM assignment WHERE command = 'reply'`)
	e.Until("the reply to start by a tick", func() bool {
		e.Clock.Add(time.Minute)
		e.Tick()
		for _, m := range e.ChatMessages(topic) {
			if m.Role == "master" && m.Text == "Hi." {
				return true
			}
		}
		return false
	})
}

// TestFall09ANewVersionFallsAfterItsMigration: an update put in place migrates the base and falls before it serves;
// the next start goes back to the binary and the base before the update, and that deck serves.
func TestFall09ANewVersionFallsAfterItsMigration(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	h := deckhome.Layout(e.Home)
	e.Project("kept", harness.PlainManifest)
	var before int64
	if err := e.SQLRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e.Stop()

	// The next deck: one migration that runs, one that fails after it.
	migrations := filepath.Join(harness.Root, "internal", "store", "migrations")
	overlay := map[string]string{
		filepath.Join(migrations, "99990_next.sql"):   "-- +goose Up\nCREATE TABLE next_table (x INTEGER);\n",
		filepath.Join(migrations, "99991_broken.sql"): "-- +goose Up\nTHIS IS NOT SQL;\n",
	}
	old, upd := buildDeck(t, nil), buildDeck(t, overlay)
	dir := t.TempDir()
	exe := filepath.Join(dir, "deck")
	copyFile(t, upd, exe)
	copyFile(t, old, exe+".old")
	pending := release.Pending{Exe: exe, Old: exe + ".old", Version: "v-next"}
	if err := os.WriteFile(h.Update(), pending.JSON(), 0o600); err != nil {
		t.Fatal(err)
	}
	// As after a hand restore of the same update before, whose steps leave update-kept.
	if err := os.WriteFile(h.UpdateKept(), []byte("99991"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, served := startDeck(t, exe, e.Home); served {
		t.Fatalf("the next deck falls on its broken migration:\n%s", out)
	}
	base := harness.OpenBase(t, h.DB())
	var migrated int64
	if err := base.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&migrated); err != nil || migrated != 99990 {
		t.Fatalf("the next deck migrated the base before it fell: %d %v", migrated, err)
	}
	base.Close()

	// The service manager starts deck again.
	if out, served := startDeck(t, exe, e.Home); !served {
		t.Fatalf("the deck before the update serves on the base before it:\n%s", out)
	}
	base = harness.OpenBase(t, h.DB())
	defer base.Close()
	var version, kept, nextTable int64
	if err := base.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil || version != before {
		t.Fatalf("the base is the one before the update, at %d: %d %v", before, version, err)
	}
	_ = base.QueryRow(`SELECT count(*) FROM project WHERE name = 'kept'`).Scan(&kept)
	_ = base.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'next_table'`).Scan(&nextTable)
	if kept != 1 || nextTable != 0 {
		t.Fatalf("the base holds what it held before the update and nothing of it: kept %d, next_table %d", kept, nextTable)
	}
	if sum(t, exe) != sum(t, old) {
		t.Fatal("the binary in place is the one before the update")
	}
	for _, gone := range []string{h.Update(), h.UpdateKept(), exe + ".old"} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s goes with the update: %v", gone, err)
		}
	}
}

// buildDeck builds deck from this tree with the files of overlay added by their paths.
func buildDeck(t *testing.T, overlay map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	args := []string{"build", "-o", filepath.Join(dir, "deck")}
	if len(overlay) > 0 {
		replace := map[string]string{}
		for path, text := range overlay {
			f := filepath.Join(dir, filepath.Base(path))
			if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			replace[path] = f
		}
		b, _ := json.Marshal(map[string]any{"Replace": replace})
		if err := os.WriteFile(filepath.Join(dir, "overlay.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-overlay", filepath.Join(dir, "overlay.json"))
	}
	cmd := exec.Command("go", append(args, "./cmd/deck")...)
	cmd.Dir = harness.Root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	return filepath.Join(dir, "deck")
}

// startDeck starts the binary at exe on home until it serves or ends; one that serves is stopped.
func startDeck(t *testing.T, exe, home string) (string, bool) {
	t.Helper()
	// Agent CLIs that fail stand before the owner's, so a self-check never runs on the owner's accounts.
	shims := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := harness.WriteProgram(filepath.Join(shims, name), []byte("#!/bin/sh\nexit 1\n")); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(exe, "-addr", "127.0.0.1:0")
	env := slices.DeleteFunc(os.Environ(), func(v string) bool { return strings.HasPrefix(v, "DECK_") })
	cmd.Env = append(env, "DECK_HOME="+home, "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var said []string
	served := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			said = append(said, sc.Text())
			if strings.Contains(sc.Text(), "deck listening") {
				served <- true
				_, _ = io.Copy(io.Discard, stderr)
				return
			}
		}
		close(served)
	}()
	ok := harness.Receive(t, "deck to serve or end", served)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	return strings.Join(said, "\n"), ok
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err == nil {
		err = os.WriteFile(to, b, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func sum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func TestFall10ABlobWithoutItsRow(t *testing.T) {
	t.Skip("row 10, A18: a blob written without its file row, or the row without the blob, is not found by any sweep")
}

// TestFall11ARestartWithACronSlotInThePast: a cron slot that passed while deck was down starts once after the start,
// however many ticks follow.
func TestFall11ARestartWithACronSlotInThePast(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	e.Project("daily", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Stop()
	e.Clock.Add(3 * time.Hour)
	e2 := e.Restart()
	for range 3 {
		e2.Tick()
		e2.Settle()
	}
	if n := e2.CountOf("job"); n != 1 {
		t.Fatalf("the slot that passed starts once: %d", n)
	}
}

// TestFall12ATransactionWaitsOnTheNetwork: a request to the network, a box or the turn of a provider opened inside a
// transaction stops at its door, before the writer stands waiting on it; after the commit the same context passes.
func TestFall12ATransactionWaitsOnTheNetwork(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(page.Close)
	egress.AsPublic(strings.TrimPrefix(page.URL, "http://"))
	doors := []struct {
		name string
		open func(ctx context.Context)
	}{
		{"egress", func(ctx context.Context) {
			if res, err := egress.Fetch(ctx, egress.With(nil, page.URL), page.URL); err == nil {
				_ = res.Body.Close()
			}
		}},
		{"a pinned request", func(ctx context.Context) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
			if req, err := egress.Pin(req, egress.With(nil, page.URL)); err == nil {
				if res, err := egress.Pinned.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}
		}},
		{"a model's connection", func(ctx context.Context) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
			if res, err := egress.Direct.RoundTrip(req); err == nil {
				_ = res.Body.Close()
			}
		}},
		{"a client of egress", func(ctx context.Context) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
			if res, err := egress.Client(egress.With(nil, page.URL), time.Minute).Do(req); err == nil {
				_ = res.Body.Close()
			}
		}},
		{"a lookup of a name", func(ctx context.Context) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:1/", nil)
			_, _ = egress.Pin(req, []string{"localhost"})
		}},
		{"a node's services", func(ctx context.Context) {
			for _, t := range []http.RoundTripper{egress.Loopback, egress.ToDeck, egress.FromNode} {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, page.URL, nil)
				if res, err := t.RoundTrip(req); err == nil {
					_ = res.Body.Close()
				}
			}
		}},
		{"a dial of a node's loopback", func(ctx context.Context) {
			if c, err := egress.DialLoopback(ctx, strings.TrimPrefix(page.URL, "http://")); err == nil {
				_ = c.Close()
			}
		}},
		{"the connection of a node", func(ctx context.Context) {
			deck, node := net.Pipe()
			defer node.Close()
			go func() { _, _ = io.Copy(io.Discard, node) }()
			cc, err := egress.NodeConn(context.WithoutCancel(ctx), deck)
			if err != nil {
				return
			}
			defer cc.Close()
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://node/", nil)
			if res, err := cc.RoundTrip(req); err == nil {
				_ = res.Body.Close()
			}
		}},
		{"deck's MCP server", func(ctx context.Context) {
			_ = e.App.Gateway.Reach(ctx, e.URL)
		}},
		{"sandbox", func(ctx context.Context) {
			if p, err := e.App.Cfg.Sandbox.Run(ctx, sandbox.Box{Kind: sandbox.KindScript, Argv: []string{"true"}, Dir: t.TempDir()}, sandbox.IO{}); err == nil {
				_ = p.Wait()
			}
		}},
		{"provider", func(ctx context.Context) {
			_, _ = provider.Run(ctx, &harness.Fake{Script: harness.Ok}, provider.Request{})
		}},
	}
	for _, d := range doors {
		var escaped context.Context
		stopped := func() (p any) {
			defer func() { p = recover() }()
			// The test opens a door inside a transaction, which no code of deck may do.
			_ = e.App.Store.Tx(t.Context(), func(ctx context.Context, _ *db.Queries) error {
				escaped = ctx
				d.open(ctx)
				return nil
			})
			return nil
		}()
		if s, _ := stopped.(string); !strings.HasPrefix(s, "txguard: ") {
			t.Errorf("%s opened inside a transaction is not stopped: %v", d.name, stopped)
			continue
		}
		d.open(escaped)
	}
}

func TestFall13AManifestChangedWhileItsMCPServerRuns(t *testing.T) {
	t.Skip("row 13, A18: an MCP server started with the old box of a changed manifest runs until deck restarts")
}

func TestFall14ATabOpenAcrossAnUpdate(t *testing.T) {
	t.Skip("row 14, A16: a tab opened before an update waits for fields the new deck no longer sends")
}

// TestFall15AHandOnCutBetweenItsTwoProcesses: deck fell after the continuation started and before the process it
// continues closed; after the start only the continuation goes on.
func TestFall15AHandOnCutBetweenItsTwoProcesses(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	clock := &harness.Clock{At: harness.T0}
	e, _, _ := harness.TwoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return harness.Limited(clock.Now().Add(time.Hour))
	}, func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}, harness.WithClock(clock))
	id := e.StartWork(1)
	e.WaitState(id, "error")
	next := e.Following(id)
	e.WaitState(next, "running")
	// handOnTo cut after the continuation started and before finish closed the process it continues.
	e.SQL(`UPDATE process SET state = 'running', finished_at = NULL, error_type = '', error = '' WHERE id = ?`, id)
	main := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	backup := &harness.Fake{ProviderName: "backup", Script: main.Script}
	e2 := e.Restart(harness.WithProvider(main, backup))
	e2.WaitState(next, "done")
	clock.Add(2 * time.Hour)
	e2.Tick()
	e2.Settle()
	if old := e2.Proc(id); old.State != "error" || len(main.Calls()) != 0 {
		t.Fatalf("only the continuation goes on after the start; the process it continues stays closed: %+v, %d turns", old, len(main.Calls()))
	}
}

// TestFall16ACallOfAToolAnsweredByTheOwner: the call of a process that waited for the owner's yes runs, once answered,
// as the same caller: its line in the journal names the run it came from.
func TestFall16ACallOfAToolAnsweredByTheOwner(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte(`{"id": "n1"}`)) })
	agent := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "items__send", map[string]any{"text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}}
	e := harness.New(t, harness.WithProvider(agent))
	e.Project("items", itemsManifest(svc.URL))
	id := e.Start("items", "work", nil)
	e.Answer(e.WaitAsk().ID, "allow")
	e.WaitState(id, "done", "error")
	var waited, ran string
	_ = e.SQLRow(`SELECT actor FROM tool_call WHERE tool = 'items.note' AND outcome = 'waiting'`).Scan(&waited)
	_ = e.SQLRow(`SELECT actor FROM tool_call WHERE tool = 'items.note' AND outcome = 'done'`).Scan(&ran)
	if waited == "" || ran != waited {
		t.Fatalf("the answered call runs as the caller that waited: waited as %q, ran as %q", waited, ran)
	}
}

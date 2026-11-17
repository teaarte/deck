package surface

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deck2/internal/egress"
	"deck2/internal/provider"
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

func TestFall01AfterTheCommitOfACallBeforeItsMark(t *testing.T) {
	t.Skip("row 1, A5: the mark of a call is written after its commit (gateway/may.go), so a fall between them leaves the text with the agent and no mark")
}

func TestFall02AnOutsideServerFailsWithForeignText(t *testing.T) {
	t.Skip("row 2, A5: whether the failure of an outside server marks the turn depends on the Go type of its error (gateway/may.go)")
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

func TestFall04AMarkedTurnLeavesFilesInTheDataFolder(t *testing.T) {
	t.Skip("row 4, A5: the files a marked turn keeps in the data folder do not mark its module")
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

func TestFall06AHookFailsToWrite(t *testing.T) {
	t.Skip("row 6, A4: the row of the outbox is deleted whatever its hooks return, so a hook that failed loses its consequence")
}

func TestFall07ARetryOfAChatAcrossARestart(t *testing.T) {
	t.Skip("row 7, A6: chat_retry keeps its provider, notes and Follows in memory, so after a restart the reply runs on the default model")
}

func TestFall08AReplyOfTheMasterThatDidNotStart(t *testing.T) {
	t.Skip("row 8, A6: a message whose reply did not start waits for a new message or a restart; PumpChats runs only at start")
}

func TestFall09ANewVersionFallsAfterItsMigration(t *testing.T) {
	t.Skip("row 9, A2: an update that migrated the base and fell before it served leaves the old deck unable to start on it")
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
	skip(t, "row 15, A11: confirmed: the process that handed on resumes beside its continuation and runs again once its model's pause ends; A11 makes the cut impossible and closes such a process at start")
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
	skip(t, "row 16, A7: a call answered by the owner runs as a live turn's: Caller.actor() loses Run, Trial and Why (journal: tool:2 waits, agent:2 runs)")
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

package providers

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const tellManifest = `{"version": 13, "commands": [{"name": "work", "title": "Work", "description": "Works a while", "class": "internal",
  "executor": {"type": "agent", "prompt": "Work."}}]}`

func TestOwnerMessageReachesTheLiveTurn(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 4)
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		started <- struct{}{}
		return provider.Result{Text: "heard: " + harness.Heard(ctx, c)}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	topic := e.Say(0, "start a long job")
	<-started
	e.Until("the live turn", func() bool {
		ms := e.ChatMessages(topic)
		return ms[0].Reply != nil && ms[0].Reply.State == "running"
	})
	e.Say(topic, "and also buy milk")
	said := e.MasterSaid(topic, 1)
	if !strings.Contains(said[0].Text, "and also buy milk") || len(fake.Calls()) != 1 {
		t.Fatalf("the message goes into the turn that runs: %+v, %d calls", said, len(fake.Calls()))
	}
	ms := e.ChatMessages(topic)
	if ms[1].Process != ms[0].Process {
		t.Fatalf("both messages belong to the one reply: %+v", ms)
	}

	p := e.Project("job", tellManifest)
	id := e.Run(p, "work")
	<-started
	e.WaitState(id, "running")
	var out struct{ Delivered string }
	e.Must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if got := e.WaitState(id, "done"); out.Delivered != "now" || !strings.Contains(string(got.Result), "use the blue folder") {
		t.Fatalf("process_tell reaches a live turn of any agent: %s %+v", out.Delivered, got)
	}
	if code, _ := e.Call("deck.process_tell", map[string]any{"process": id, "text": "late"}); code != 422 {
		t.Fatal("a finished process takes a remark, not a message")
	}
}

func TestMessageWaitsForAProviderWithoutLiveInput(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return provider.Result{Text: "first done"}
		}
		return provider.Result{Text: "then: " + harness.LastLine(strings.Split(c.Req.Prompt, "Continue the task.")[0])}
	}))

	p := e.Project("job", tellManifest)
	id := e.Run(p, "work")
	e.Until("the turn", func() bool { return len(e.Fake.Calls()) == 1 })
	var out struct{ Delivered string }
	e.Must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if out.Delivered != "after the turn" {
		t.Fatalf("a provider without live input gets the message after its turn: %+v", out)
	}
	close(release)
	got := e.WaitState(id, "done")
	calls := e.Fake.Calls()
	if len(calls) != 2 || !calls[1].Resume || !strings.Contains(calls[1].Prompt, "The owner wrote while you worked:\nuse the blue folder") ||
		!strings.Contains(string(got.Result), "use the blue folder") {
		t.Fatalf("the turn ends, the same session goes on with the message: %d calls %+v", len(calls), got)
	}
	var asks []harness.Ask
	e.Must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("a message of the owner is not something that waits for the owner: %+v", asks)
	}
}

func TestTerminalCommandOfAProcess(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		<-release
		return provider.Result{Text: "ok"}
	}))

	p := e.Project("job", tellManifest)
	id := e.Run(p, "work")
	e.Until("the turn", func() bool { return len(e.Fake.Calls()) == 1 })
	var v struct {
		Terminal string `json:"terminal"`
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &v)
	session := e.Fake.Calls()[0].SessionID
	work := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(id), "work")
	if v.Terminal != "cd '"+work+"' && fake --resume "+session {
		t.Fatalf("the folder of the turn and the command that goes on with its session: %q", v.Terminal)
	}
	close(release)
	e.WaitState(id, "done")
	_ = os.RemoveAll(work)
	e.Must("deck.process_get", map[string]any{"process": id}, &v)
	if v.Terminal != "fake --resume "+session {
		t.Fatalf("without the folder only the command: %q", v.Terminal)
	}
}

func TestClaudeCodeTakesTheOwnersMessageIntoItsTurn(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	before := `sed -n 1p "$d/stream" | sed "s/{{session}}/$session/g"; : > "$c/ready"; i=0; while [ "$(grep -c '"type":"user"' "$c/stdin")" -lt 2 ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done` + "\n"
	if err := os.WriteFile(filepath.Join(c.Dir, "before"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	harness.Until(t, "the CLI to start", func() bool { return c.Calls() >= 1 })
	e.Until("the CLI to report its session", func() bool {
		return c.Files(1)("ready") != "" || fileExists(filepath.Join(c.Dir, "calls", "1", "ready"))
	})
	// Deck reads the session line of the CLI; no read shows that.
	time.Sleep(200 * time.Millisecond)
	var out struct{ Delivered string }
	e.Must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if got := e.WaitState(id, "done", "error"); got.State != "done" || out.Delivered != "now" {
		t.Fatalf("the running CLI takes the message and the turn ends with its one result: %s %+v", out.Delivered, got)
	}
	stdin := c.Files(1)("stdin")
	if c.Calls() != 1 || !strings.Contains(stdin, `"content":"deck: the owner wrote while you work:\nuse the blue folder"`) {
		t.Fatalf("the message goes to stdin of the running CLI as a user line: %d calls\n%s", c.Calls(), stdin)
	}
}

func TestABackgroundTaskOfClaudeCodeEndsTheTurnWithItsLastResult(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "background.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("plain", harness.PlainManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `"background said done"` {
		t.Fatalf("a background task that wakes the session gives one more result, and the last one counts: %+v", got)
	}
}

// TestAnEmptyResultWhileBackgroundAgentsRunIsNotTheEndOfTheTurn: Claude Code hands in a result at once after it
// sends helpers to the background; deck keeps the CLI until they report and takes the result that follows.
func TestAnEmptyResultWhileBackgroundAgentsRunIsNotTheEndOfTheTurn(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "background_agent.jsonl")
	rest := `{"type": "system", "subtype": "task_notification", "task_id": "bg1", "status": "completed", "session_id": "{{session}}"}
{"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "agents said done"}]}, "session_id": "{{session}}"}
{"type": "result", "subtype": "success", "is_error": false, "result": "agents said done", "session_id": "{{session}}"}
`
	if err := os.WriteFile(filepath.Join(c.Dir, "rest"), []byte(rest), 0o644); err != nil {
		t.Fatal(err)
	}
	c.After(`sleep 1; sed "s/{{session}}/$session/g" "$d/rest"` + "\n")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("plain", harness.PlainManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `"agents said done"` {
		t.Fatalf("the CLI outlives its linger while its background agent runs, and the later result counts: %+v", got)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

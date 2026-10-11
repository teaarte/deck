package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

const tellManifest = `{"version": 12, "commands": [{"name": "work", "title": "Work", "description": "Works a while", "class": "internal",
  "executor": {"type": "agent", "prompt": "Work."}}]}`

// heard waits for a message of the owner on the live channel of a turn and takes it.
func heard(ctx context.Context, c *Call) string {
	select {
	case m := <-c.Req.Live:
		m.Taken <- true
		return m.Text
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
	return ""
}

func TestOwnerMessageReachesTheLiveTurn(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 4)
	fake := &Fake{live: true, script: func(ctx context.Context, c *Call) provider.Result {
		started <- struct{}{}
		return provider.Result{Text: "heard: " + heard(ctx, c)}
	}}
	e := newEnvWith(t, t.TempDir(), fake)
	topic := e.say(0, "start a long job")
	<-started
	e.waitFor("the live turn", func() bool {
		ms := e.chatMessages(topic)
		return ms[0].Reply != nil && ms[0].Reply.State == "running"
	})
	e.say(topic, "and also buy milk")
	said := e.masterSaid(topic, 1)
	if !strings.Contains(said[0].Text, "and also buy milk") || len(fake.Calls()) != 1 {
		t.Fatalf("the message goes into the turn that runs: %+v, %d calls", said, len(fake.Calls()))
	}
	ms := e.chatMessages(topic)
	if ms[1].Process != ms[0].Process {
		t.Fatalf("both messages belong to the one reply: %+v", ms)
	}

	p := e.createProject("job", tellManifest)
	id := e.run(p, "work")
	<-started
	e.waitState(id, "running")
	var out struct{ Delivered string }
	e.must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if got := e.waitState(id, "done"); out.Delivered != "now" || !strings.Contains(string(got.Result), "use the blue folder") {
		t.Fatalf("process_tell reaches a live turn of any agent: %s %+v", out.Delivered, got)
	}
	if code, _ := e.call("deck.process_tell", map[string]any{"process": id, "text": "late"}); code != 422 {
		t.Fatal("a finished process takes a remark, not a message")
	}
}

func TestMessageWaitsForAProviderWithoutLiveInput(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return provider.Result{Text: "first done"}
		}
		return provider.Result{Text: "then: " + lastLine(strings.Split(c.Req.Prompt, "Continue the task.")[0])}
	})
	p := e.createProject("job", tellManifest)
	id := e.run(p, "work")
	e.waitFor("the turn", func() bool { return len(e.Fake.Calls()) == 1 })
	var out struct{ Delivered string }
	e.must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if out.Delivered != "after the turn" {
		t.Fatalf("a provider without live input gets the message after its turn: %+v", out)
	}
	close(release)
	got := e.waitState(id, "done")
	calls := e.Fake.Calls()
	if len(calls) != 2 || !calls[1].Resume || !strings.Contains(calls[1].Prompt, "The owner wrote while you worked:\nuse the blue folder") ||
		!strings.Contains(string(got.Result), "use the blue folder") {
		t.Fatalf("the turn ends, the same session goes on with the message: %d calls %+v", len(calls), got)
	}
	var asks []askView
	e.must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("a message of the owner is not something that waits for the owner: %+v", asks)
	}
}

func TestTerminalCommandOfAProcess(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-release
		return provider.Result{Text: "ok"}
	})
	p := e.createProject("job", tellManifest)
	id := e.run(p, "work")
	e.waitFor("the turn", func() bool { return len(e.Fake.Calls()) == 1 })
	var v struct {
		Terminal string `json:"terminal"`
	}
	e.must("deck.process_get", map[string]any{"process": id}, &v)
	session := e.Fake.Calls()[0].SessionID
	work := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(id), "work")
	if v.Terminal != "cd '"+work+"' && fake --resume "+session {
		t.Fatalf("the folder of the turn and the command that goes on with its session: %q", v.Terminal)
	}
	close(release)
	e.waitState(id, "done")
	_ = os.RemoveAll(work)
	e.must("deck.process_get", map[string]any{"process": id}, &v)
	if v.Terminal != "fake --resume "+session {
		t.Fatalf("without the folder only the command: %q", v.Terminal)
	}
}

func TestClaudeCodeTakesTheOwnersMessageIntoItsTurn(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	before := `sed -n 1p "$d/stream" | sed "s/{{session}}/$session/g"; : > "$c/ready"; i=0; while [ "$(grep -c '"type":"user"' "$c/stdin")" -lt 2 ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done` + "\n"
	if err := os.WriteFile(filepath.Join(c.dir, "before"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	waitCalls(t, c, 1)
	e.waitFor("the CLI to report its session", func() bool {
		return c.files(1)("ready") != "" || fileExists(filepath.Join(c.dir, "calls", "1", "ready"))
	})
	time.Sleep(200 * time.Millisecond)
	var out struct{ Delivered string }
	e.must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, &out)
	if got := e.waitState(id, "done", "error"); got.State != "done" || out.Delivered != "now" {
		t.Fatalf("the running CLI takes the message and the turn ends with its one result: %s %+v", out.Delivered, got)
	}
	stdin := c.files(1)("stdin")
	if c.calls() != 1 || !strings.Contains(stdin, `"content":"deck: the owner wrote while you work:\nuse the blue folder"`) {
		t.Fatalf("the message goes to stdin of the running CLI as a user line: %d calls\n%s", c.calls(), stdin)
	}
}

func TestABackgroundTaskOfClaudeCodeEndsTheTurnWithItsLastResult(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "background.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `"background said done"` {
		t.Fatalf("a background task that wakes the session gives one more result, and the last one counts: %+v", got)
	}
}

// TestAnEmptyResultWhileBackgroundAgentsRunIsNotTheEndOfTheTurn: Claude Code hands in a result at once after it
// sends helpers to the background; deck keeps the CLI until they report and takes the result that follows.
func TestAnEmptyResultWhileBackgroundAgentsRunIsNotTheEndOfTheTurn(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "background_agent.jsonl")
	rest := `{"type": "system", "subtype": "task_notification", "task_id": "bg1", "status": "completed", "session_id": "{{session}}"}
{"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "agents said done"}]}, "session_id": "{{session}}"}
{"type": "result", "subtype": "success", "is_error": false, "result": "agents said done", "session_id": "{{session}}"}
`
	if err := os.WriteFile(filepath.Join(c.dir, "rest"), []byte(rest), 0o644); err != nil {
		t.Fatal(err)
	}
	c.after(`sleep 1; sed "s/{{session}}/$session/g" "$d/rest"` + "\n")
	e, _ := cliEnv(t, c)
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `"agents said done"` {
		t.Fatalf("the CLI outlives its linger while its background agent runs, and the later result counts: %+v", got)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

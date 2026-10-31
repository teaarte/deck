package scenarios

import (
	"slices"
	"strings"
	"testing"
)

// An agent a signal from outside deck killed mid-turn goes on once in the same session and keeps what it did.
func TestAnAgentKilledFromOutsideGoesOnInItsSession(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	c.before(`if [ "$n" = 1 ]; then sed "s/{{session}}/$session/g" "$d/stream" | head -2; exit 143; fi`)
	e := New(t, WithCLI(c))
	p := e.Project("plain", plainManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the killed turn goes on in its session: %+v", got)
	}
	if args := c.call(2).args; !slices.Contains(args, "--resume") || !slices.Contains(args, e.session(id)) {
		t.Fatalf("the second start resumes the session: %v", args)
	}
	if stdin := c.call(2).stdin; !strings.Contains(stdin, "signal") {
		t.Fatalf("the agent is told its turn was killed: %s", stdin)
	}
}

// An agent killed again after it went on fails, and says which signal killed it and that deck did not send it.
func TestAnAgentKilledTwiceSaysWhoKilledIt(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	c.before(`sed "s/{{session}}/$session/g" "$d/stream" | head -2; kill -TERM $$`)
	e := New(t, WithCLI(c))
	p := e.Project("plain", plainManifest)
	got := e.WaitState(e.Run(p, "work"), "done", "error")
	if got.State != "error" || c.calls() != 2 || !strings.Contains(got.Error, "SIGTERM") || !strings.Contains(got.Error, "not deck") {
		t.Fatalf("a turn killed twice fails once, naming the signal and that it came from outside deck: %d calls, %+v", c.calls(), got)
	}
}

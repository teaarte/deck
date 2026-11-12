package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
)

// askModule calls its external publish from a script, and hears how the confirmation closed with heard when on.
func askModule(on bool) string {
	trigger := ""
	if on {
		trigger = `, "triggers": [{"command": "heard", "on": "ask"}]`
	}
	return harness.Manifest(fmt.Sprintf(`{"commands": [
    {"name": "run", "title": "Run", "description": "Asks to publish.", "result": {"type": ["object", "null"]},
     "executor": {"type": "script", "script": "main"}},
    {"name": "publish", "title": "Publish", "description": "Publishes outside.",
     "executor": {"type": "agent", "prompt": "Publish."}},
    {"name": "heard", "title": "Heard", "description": "Hears the owner's answer.", "result": {"type": "object"},
     "input": {"type": "object", "properties": {"ask": {"type": "integer"}, "answer": {"type": "string", "enum": ["yes", "no", "expired"]},
       "process": {"type": "integer", "format": "process"}}, "required": ["ask", "answer", "process"]},
     "executor": {"type": "script", "script": "heard"}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"},
    {"name": "heard", "file": "scripts/heard.py", "runtime": "python"}],
  "rights": [{"right": "send", "what": ["publish"], "why": "publishes outside"}]%s}`, trigger))
}

// heardRuns are the inputs the runs of heard of a project got, by ask.
func heardRuns(e *harness.Env, project string) map[int64]map[string]any {
	out := map[int64]map[string]any{}
	for _, p := range e.Procs() {
		if p.Project != project || p.Command != "heard" || p.State != "done" {
			continue
		}
		var in map[string]any
		if json.Unmarshal(p.Result, &in) == nil {
			out[int64(in["ask"].(float64))] = in
		}
	}
	return out
}

func TestAModuleHearsTheOwnersAnswerToItsConfirmation(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock(), harness.WithConfig(harness.WithTTL))
	bare := strings.Replace(askModule(true), `"required": ["ask", "answer", "process"]`, `"required": ["process"]`, 1)
	bare = strings.Replace(bare, `"ask": {"type": "integer"}, `, "", 1)
	if code, out := e.Call("deck.project_create", map[string]any{"name": "bare", "manifest": bare}); code != 422 || !strings.Contains(string(out), `"ask"`) {
		t.Fatalf("a command a trigger on ask starts declares the field ask: %d %s", code, out)
	}
	outward := strings.Replace(askModule(true), `"what": ["publish"]`, `"what": ["publish", "heard"]`, 1)
	if code, out := e.Call("deck.project_create", map[string]any{"name": "outward", "manifest": outward}); code != 422 || !strings.Contains(string(out), "needs the right send") {
		t.Fatalf("a trigger on ask starts no external command, which would go without the owner: %d %s", code, out)
	}
	for _, name := range []string{"jobs", "other"} {
		e.Project(name, askModule(name == "jobs"))
		e.SaveScript(name, "scripts/main.py", harness.PythonLib+fmt.Sprintf(`
st, out = deck(%q, {})
print("null" if st == 202 else json.dumps({"status": st}))
`, name+".publish"))
		e.SaveScript(name, "scripts/heard.py", harness.PythonLib+`
print(json.dumps(json.load(sys.stdin)))
`)
	}
	ask := func(project string) (int64, int64) {
		id := e.Start(project, "run", nil)
		e.WaitState(id, "done", "error")
		var a int64
		e.Until("the call to wait for the owner", func() bool {
			var as []struct{ ID, Process int64 }
			e.Must("deck.asks_list", nil, &as)
			for _, x := range as {
				if x.Process == id {
					a = x.ID
				}
			}
			return a != 0
		})
		return a, id
	}

	yes, yesBy := ask("jobs")
	e.Answer(yes, "allow")
	no, noBy := ask("jobs")
	e.Answer(no, "deny")
	others, _ := ask("other")
	e.Answer(others, "deny")
	late, lateBy := ask("jobs")
	e.Until("the module to hear yes and no", func() bool { return len(heardRuns(e, "jobs")) == 2 })
	e.Clock.Add(25 * time.Hour)
	e.App.Sweep(context.Background(), e.Clock.Now())

	var runs map[int64]map[string]any
	e.Until("the module to hear its three asks", func() bool { runs = heardRuns(e, "jobs"); return len(runs) == 3 })
	for a, want := range map[int64]struct {
		answer string
		by     int64
	}{yes: {"yes", yesBy}, no: {"no", noBy}, late: {"expired", lateBy}} {
		if got := runs[a]; got["answer"] != want.answer || got["process"] != float64(want.by) {
			t.Fatalf("ask %d closed as %s heard from process %d: %+v", a, want.answer, want.by, got)
		}
	}
	if len(heardRuns(e, "other")) != 0 {
		t.Fatalf("a module without a trigger on ask hears nothing")
	}

	e.App.Answers.Heard(context.Background())
	e.App.Sweep(context.Background(), e.Clock.Now())
	if again := heardRuns(e, "jobs"); len(again) != 3 || runs[others] != nil {
		t.Fatalf("an ask is heard once, and only by the module whose process asked: %+v", again)
	}
	n := 0
	for _, p := range e.Procs() {
		if p.Command == "heard" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("each ask starts heard once: %d runs", n)
	}
}

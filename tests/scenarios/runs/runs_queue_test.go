package runs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// spanScript notes when it began and ended in the data folder of its module, a second apart.
const spanScript = `import os, time
p = os.path.join(os.environ["DECK_DATA"], "spans")
with open(p, "a") as f: f.write("start %f\n" % time.time())
time.sleep(1)
with open(p, "a") as f: f.write("end %f\n" % time.time())
print("{}")
`

// besideScript notes its start, waits up to a minute for a second run to start too, and notes its end: runs side by
// side both start before either ends, however slow the machine.
const besideScript = `import os, time
p = os.path.join(os.environ["DECK_DATA"], "spans")
with open(p, "a") as f: f.write("start %f\n" % time.time())
deadline = time.time() + 60
while time.time() < deadline and open(p).read().count("start") < 2: time.sleep(0.05)
with open(p, "a") as f: f.write("end %f\n" % time.time())
print("{}")
`

// A command runs one at a time unless it says runs many: a second call while the first runs starts once the first
// has ended.
func TestACommandOfOneRunAtATimeQueuesTheNextCall(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("spans", harness.ScriptManifest("run", "", ""))
	e.SaveScript("spans", "scripts/main.py", spanScript)
	first, second := e.Start("spans", "run", nil), e.Start("spans", "run", nil)
	if first == second {
		t.Fatalf("a second call is a run of its own: %d", first)
	}
	if got := e.WaitState(second, "waiting:run", "done"); got.State != "waiting:run" {
		t.Fatalf("the second run waits for the first: %+v", got)
	}
	for _, id := range []int64{first, second} {
		if got := e.WaitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("each run is done: %+v", got)
		}
	}
	b, _ := os.ReadFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Data(p), "spans"))
	var at []float64
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		_, v, _ := strings.Cut(l, " ")
		f, _ := strconv.ParseFloat(v, 64)
		at = append(at, f)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(at) != 4 || !strings.HasPrefix(lines[1], "end") || at[2] < at[1] {
		t.Fatalf("the second run starts after the first ended:\n%s", b)
	}
}

// A command of runs many runs a second call beside the first.
func TestACommandOfManyRunsRunsTheNextCallBeside(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("spans", harness.Many(harness.ScriptManifest("run", "", "")))
	e.SaveScript("spans", "scripts/main.py", besideScript)
	first, second := e.Start("spans", "run", nil), e.Start("spans", "run", nil)
	for _, id := range []int64{first, second} {
		if got := e.WaitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("each run is done: %+v", got)
		}
	}
	b, _ := os.ReadFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Data(p), "spans"))
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) != 4 || !strings.HasPrefix(lines[1], "start") {
		t.Fatalf("the second run starts before the first ended:\n%s", b)
	}
}

// A run stopped by the owner lets the next one of its command start at once.
func TestAStoppedRunLetsTheNextOneStart(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("spans", strings.Replace(harness.ScriptManifest("run", "", ""), `"class": "internal",`, `"class": "internal", "runs": "one",`, 1))
	e.SaveScript("spans", "scripts/main.py", strings.Replace(spanScript, "time.sleep(1)", "time.sleep(30)", 1))
	first, second := e.Start("spans", "run", nil), e.Start("spans", "run", nil)
	e.WaitState(second, "waiting:run")
	e.Must("deck.process_stop", map[string]any{"process": first}, nil)
	if got := e.WaitState(second, "running"); got.State != "running" {
		t.Fatalf("the next run starts once the first is stopped: %+v", got)
	}
	e.Must("deck.process_stop", map[string]any{"process": second}, nil)
}

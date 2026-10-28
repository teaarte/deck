package scenarios

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
)

// spanScript notes when it began and ended in the data folder of its module, a second apart.
const spanScript = `import os, time
p = os.path.join(os.environ["DECK_DATA"], "spans")
with open(p, "a") as f: f.write("start %f\n" % time.time())
time.sleep(1)
with open(p, "a") as f: f.write("end %f\n" % time.time())
print("{}")
`

// A command that runs one at a time: a second call while the first runs starts once the first has ended.
func TestACommandOfOneRunAtATimeQueuesTheNextCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	p := e.createProject("spans", strings.Replace(scriptManifest("run", "", ""), `"class": "internal",`, `"class": "internal", "runs": "one",`, 1))
	e.saveScript("spans", "scripts/main.py", spanScript)
	first, second := e.start("spans", "run", nil), e.start("spans", "run", nil)
	if first == second {
		t.Fatalf("a second call is a run of its own: %d", first)
	}
	if got := e.waitScript(second, "waiting_run", "done"); got.State != "waiting_run" {
		t.Fatalf("the second run waits for the first: %+v", got)
	}
	for _, id := range []int64{first, second} {
		if got := e.waitScript(id, "done", "error"); got.State != "done" {
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

// A run stopped by the owner lets the next one of its command start at once.
func TestAStoppedRunLetsTheNextOneStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("spans", strings.Replace(scriptManifest("run", "", ""), `"class": "internal",`, `"class": "internal", "runs": "one",`, 1))
	e.saveScript("spans", "scripts/main.py", strings.Replace(spanScript, "time.sleep(1)", "time.sleep(30)", 1))
	first, second := e.start("spans", "run", nil), e.start("spans", "run", nil)
	e.waitScript(second, "waiting_run")
	e.must("deck.process_stop", map[string]any{"process": first}, nil)
	if got := e.pollState(second, 5*time.Second, 50*time.Millisecond, "running"); got.State != "running" {
		t.Fatalf("the next run starts once the first is stopped: %+v", got)
	}
	e.must("deck.process_stop", map[string]any{"process": second}, nil)
}

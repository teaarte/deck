package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jobManifest(url string) string {
	return fmt.Sprintf(`{"version": 7,
  "collections": [{"name": "jobs", "description": "A generation job.", "key": "topic",
    "schema": {"type": "object", "properties": {"topic": {"type": "string"}, "job": {"type": "string"}, "status": {"type": "string"}},
      "required": ["topic"]}}],
  "commands": [
    {"name": "start", "title": "Start", "description": "Starts a job and keeps its id.", "class": "internal",
     "input": {"type": "object", "properties": {"topic": {"type": "string"}}}, "result": {"type": "object"},
     "executor": {"type": "script", "script": "start"}},
    {"name": "check", "title": "Check", "description": "Checks waiting jobs and downloads what is ready.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "check"}}],
  "scripts": [{"name": "start", "file": "scripts/start.py", "runtime": "python"},
    {"name": "check", "file": "scripts/check.py", "runtime": "python"}],
  "triggers": [{"command": "check", "cron": "*/5 * * * *"}],
  "tools": {"http": [
    {"name": "create_job", "description": "Starts a job", "method": "POST", "url": "%[1]s/jobs",
     "auth": {"type": "secret", "secret": "svc"}, "input": {"type": "object", "properties": {"topic": {"type": "string"}}}},
    {"name": "job_status", "description": "Job status", "method": "GET", "url": "%[1]s/jobs/{job}", "class": "internal",
     "auth": {"type": "secret", "secret": "svc"}, "input": {"type": "object", "properties": {"job": {"type": "string"}}, "required": ["job"]},
     "result": {"type": "object", "properties": {"status": {"type": "string"}}, "required": ["status"]}},
    {"name": "job_file", "description": "Job file", "method": "GET", "url": "%[1]s/jobs/{job}/file", "class": "internal",
     "auth": {"type": "secret", "secret": "svc"}, "input": {"type": "object", "properties": {"job": {"type": "string"}}, "required": ["job"]}}]},
  "allowed_external": ["create_job"]}`, url)
}

const startScript = pythonLib + `
inp = json.load(sys.stdin)
st, job = deck("clips.create_job", {"topic": inp["topic"]})
if st != 200:
    raise SystemExit("create_job: %s %s" % (st, job))
deck("deck.records_upsert", {"collection": "jobs", "records": json.dumps({"topic": inp["topic"], "job": job["id"], "status": "waiting"})})
print(json.dumps({"job": job["id"], "secret_seen": any("tok-123" in v for v in os.environ.values())}))
`

const checkScript = pythonLib + `
st, recs = deck("deck.records_list", {"collection": "jobs"})
files = []
for r in recs:
    d = r["data"]
    if d.get("status") != "waiting":
        continue
    st, s = deck("clips.job_status", {"job": d["job"]})
    if st != 200 or s["status"] != "done":
        continue
    st, f = deck("clips.job_file", {"job": d["job"]})
    d["status"] = "done"
    deck("deck.records_upsert", {"collection": "jobs", "records": json.dumps(d)})
    files.append({"path": f["file"], "record": r["id"]})
print(json.dumps({"files": files}))
`

func TestExternalJobSurvivesARestartAndDownloadsTheFile(t *testing.T) {
	var creates, polls atomic.Int32
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/jobs":
			creates.Add(1)
			jsonReply(w, `{"id": "j1"}`)
		case r.URL.Path == "/jobs/j1":
			if polls.Add(1) == 1 {
				jsonReply(w, `{"status": "running"}`)
			} else {
				jsonReply(w, `{"status": "done"}`)
			}
		case r.URL.Path == "/jobs/j1/file":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4DATA"))
		default:
			http.NotFound(w, r)
		}
	})
	home := t.TempDir()
	clock := &Clock{now: time.Date(2026, 9, 25, 8, 1, 0, 0, time.Local)}
	e := newEnvWith(t, home, &Fake{}, withClock(clock))
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "tok-123"}, nil)
	e.createProject("clips", jobManifest(svc.URL))
	e.saveScript("clips", "scripts/start.py", startScript)
	e.saveScript("clips", "scripts/check.py", checkScript)

	started := e.waitScript(e.start("clips", "start", map[string]any{"topic": "cats"}), "done", "error")
	if started.State != "done" || !strings.Contains(string(started.Result), `"job":"j1"`) || strings.Contains(string(started.Result), `"secret_seen":true`) {
		t.Fatalf("the start script keeps the job id without seeing the secret: %+v", started)
	}
	if got := e.waitScript(e.start("clips", "check", nil), "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), `"files":[]`) {
		t.Fatalf("a job still running gives no file: %+v", got)
	}
	_ = e.App.Close()

	e2 := newEnvWith(t, home, &Fake{}, withClock(clock))
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Add(5 * time.Minute)
	e2.App.Scheduler.Tick(context.Background())
	var check procFull
	e2.waitFor("the scheduled check after the restart", func() bool {
		for _, p := range e2.processes() {
			if p.Command == "check" && p.Trigger != 0 && (p.State == "done" || p.State == "error") {
				check = p
				return true
			}
		}
		return false
	})
	var recs []struct {
		ID   int64
		Data map[string]any
	}
	e2.must("deck.records_list", map[string]any{"project": "clips", "collection": "jobs"}, &recs)
	var files []struct {
		Type string
		Size int64
	}
	e2.must("deck.files_list", map[string]any{"record": recs[0].ID}, &files)
	if check.State != "done" || recs[0].Data["status"] != "done" || len(files) != 1 || files[0].Type != "video/mp4" || files[0].Size != 7 {
		t.Fatalf("after the restart the check downloads the file to the record: %+v %+v %+v", check, recs, files)
	}
	if creates.Load() != 1 {
		t.Fatalf("the job is started once: %d", creates.Load())
	}
	var leaked int
	_ = e2.App.Store.DB.QueryRow(`SELECT count(*) FROM tool_call WHERE params LIKE '%tok-123%' OR error LIKE '%tok-123%'`).Scan(&leaked)
	var n int
	_ = e2.App.Store.DB.QueryRow(`SELECT count(*) FROM tool_call WHERE tool = 'clips.create_job' AND outcome = 'done' AND external_id = 'j1'`).Scan(&n)
	if leaked != 0 || n != 1 {
		t.Fatalf("the journal has the external call with its outside id and never the secret: leaked %d, done %d", leaked, n)
	}
}

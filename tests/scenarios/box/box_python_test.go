package box

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"deck2/internal/home"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

// index is a package index of one wheel, deckfixture, and the host its manifest names under network.
func index(t *testing.T) (url, host string) {
	whl := harness.Wheel(t, "deckfixture")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/deckfixture/":
			fmt.Fprintf(w, `<a href="/files/%s">%s</a>`, filepath.Base(whl), filepath.Base(whl))
		case "/files/" + filepath.Base(whl):
			http.ServeFile(w, r, whl)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, strings.TrimPrefix(srv.URL, "http://")
}

func needUV(t *testing.T) {
	for _, p := range []string{"uv", "python3"} {
		if _, err := sandbox.Find(p); err != nil {
			t.Skipf("%s is not installed", p)
		}
	}
}

// environments are the environments uv keeps for the scripts of a project.
func environments(t *testing.T, e *harness.Env, project string) []string {
	t.Helper()
	dir := filepath.Join(home.Layout(e.App.Cfg.Home).Data(e.ProjectID(project)), ".cache", "uv", "environments-v2")
	ents, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, d := range ents {
		out = append(out, d.Name())
	}
	return out
}

const headedScript = `# /// script
# dependencies = ["deckfixture"]
# [tool.uv]
# index-url = "%s/simple"
# ///
import json, sys, deckfixture
print(json.dumps({"value": deckfixture.VALUE, "python": sys.version_info[:2]%s}))
`

// TestAScriptRunTwiceKeepsOneEnvironment: runs of one script, by its shebang or runtime python, share one uv
// environment by its file kept in the project; a change of its header rebuilds it.
func TestAScriptRunTwiceKeepsOneEnvironment(t *testing.T) {
	t.Parallel()
	needUV(t)
	url, host := index(t)
	e := harness.New(t)
	for _, c := range []struct{ project, runtime, shebang string }{
		{"shebang", `"runtime": "exec", "programs": ["uv"]`, "#!/usr/bin/env -S uv run --script\n"},
		{"python", `"runtime": "python"`, ""},
	} {
		m := strings.Replace(harness.ScriptManifest("use", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": [%q], "why": "reads it"}]`, host)),
			`"runtime": "python"`, c.runtime, 1)
		e.Project(c.project, m)
		e.SaveScript(c.project, "scripts/main.py", c.shebang+fmt.Sprintf(headedScript, url, ""))
		for i := range 2 {
			if p := e.WaitState(e.Start(c.project, "use", nil), "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), `"value":42`) {
				t.Fatalf("%s run %d: the script gets the packages of its PEP 723 block: %+v", c.project, i, p)
			}
		}
		if envs := environments(t, e, c.project); len(envs) != 1 {
			t.Fatalf("%s: two runs of one script share one environment of uv: %v", c.project, envs)
		}
		file := filepath.Join(home.Layout(e.App.Cfg.Home).Project(e.ProjectID(c.project)), "scripts", "main.py")
		if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), "deckfixture") {
			t.Fatalf("%s: the file of the script stays in the folder of the project: %v", c.project, err)
		}

		e.SaveScript(c.project, "scripts/main.py", c.shebang+strings.Replace(fmt.Sprintf(headedScript, url, `, "six": 1`), "# dependencies", "# requires-python = \">=3.9\"\n# dependencies", 1))
		ids := []int64{e.Start(c.project, "use", nil), e.Start(c.project, "use", nil)}
		for _, id := range ids {
			if p := e.WaitState(id, "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), `"six":1`) {
				t.Fatalf("%s: runs side by side after a change of the header run the new file whole: %+v %v", c.project, p, e.Log(id))
			}
		}
		if envs := environments(t, e, c.project); len(envs) != 1 {
			t.Fatalf("%s: a change of the header rebuilds the one environment of the script: %v", c.project, envs)
		}
	}
}

// TestAPythonScriptRunsByUVOnTheOnePythonOfDeck: a script of runtime python without a PEP 723 block runs on the one
// python health names; one that asks for a newer python fails as a missing program that says both versions.
func TestAPythonScriptRunsByUVOnTheOnePythonOfDeck(t *testing.T) {
	t.Parallel()
	needUV(t)
	e := harness.New(t)
	e.Project("plain", harness.ScriptManifest("use", "", ""))
	e.SaveScript("plain", "scripts/main.py", `import json, os, sys
print(json.dumps({"python": sys.executable, "downloads": os.environ.get("UV_PYTHON_DOWNLOADS")}))
`)
	p := e.WaitState(e.Start("plain", "use", nil), "done", "error")
	var got struct{ Python, Downloads string }
	if p.State != "done" || json.Unmarshal(p.Result, &got) != nil || got.Downloads != "never" {
		t.Fatalf("a script without a PEP 723 block runs, and uv downloads no python: %+v", p)
	}
	row := e.HealthRowOf("scripts")
	text, _ := row["error"].(string)
	if row["status"] != "ok" || !strings.Contains(text, "python 3.") || !strings.Contains(text, " through uv ") {
		t.Fatalf("health names the python and uv of scripts: %+v", row)
	}
	if real, _ := filepath.EvalSymlinks(got.Python); !strings.Contains(text, got.Python) && !strings.Contains(text, real) {
		t.Fatalf("the script runs on the python health names: %s, %+v", got.Python, row)
	}

	e.SaveScript("plain", "scripts/main.py", `# /// script
# requires-python = ">=3.99"
# dependencies = []
# ///
import sys
assert sys.version_info >= (3, 99), "needs python 3.99"
`)
	p = e.WaitState(e.Start("plain", "use", nil), "done", "error")
	if f := e.Fault(p.ID); p.State != "error" || f.ErrorType != "program.missing" || !strings.Contains(p.Error, "asks for Python >=3.99; deck runs Python 3.") {
		t.Fatalf("a script that asks for a newer python fails as a missing program with both versions: %+v %+v", p, f)
	}
}

// TestRunsOfTheSameScriptSideBySideShareItsFile: many runs of one changed script started at once all run it whole.
func TestRunsOfTheSameScriptSideBySideShareItsFile(t *testing.T) {
	t.Parallel()
	needUV(t)
	e := harness.New(t)
	e.Project("many", harness.ScriptManifest("use", "", ""))
	body := `import json
print(json.dumps({"n": %d, "pad": "%s"}))
`
	for round := range 2 {
		e.SaveScript("many", "scripts/main.py", fmt.Sprintf(body, round, strings.Repeat("x", 200000)))
		var wg sync.WaitGroup
		ids := make([]int64, 4)
		for i := range ids {
			wg.Go(func() { ids[i] = e.Start("many", "use", nil) })
		}
		wg.Wait()
		for _, id := range ids {
			if p := e.WaitState(id, "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), fmt.Sprintf(`"n":%d`, round)) {
				t.Fatalf("round %d: each run reads the whole file of the script: %+v", round, p)
			}
		}
	}
}

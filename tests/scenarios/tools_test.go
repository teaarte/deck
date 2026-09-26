package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
)

func toolManifest(url, extra string) string {
	return fmt.Sprintf(`{"version": 4,
  "commands": [
    {"name": "fetch", "title": "Fetch", "description": "Fetches a picture.", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "picture"}},
    {"name": "send", "title": "Send", "description": "Sends a note.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}},
     "result": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]},
     "executor": {"type": "tool", "tool": "note"}},
    {"name": "merge", "title": "Merge", "description": "Merges the work.", "class": "reversible",
     "executor": {"type": "agent", "prompt": "Merge."}},
    {"name": "work", "title": "Work", "description": "Works.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}}],
  "tools": {"http": [
    {"name": "picture", "description": "A picture", "method": "GET", "url": "%[1]s/pics/{name}?size={size}", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}}},
    {"name": "note", "description": "Sends a note", "method": "POST", "url": "%[1]s/notes",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}}}]}%[2]s}`, url, extra)
}

func TestToolCommandMakesOneToolCall(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if strings.HasPrefix(r.URL.Path, "/pics/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGDATA"))
			return
		}
		jsonReply(w, `{"id": "n1"}`)
	})
	e := newEnv(t, nil)
	e.createProject("pics", toolManifest(svc.URL, ""))
	got := e.waitState(e.start("pics", "fetch", map[string]any{"name": "a&b=1", "size": "big-a&b=1"}), "done", "error")
	if got.State != "done" || !strings.Contains(string(got.Result), `"type":"image/png"`) || svc.requests()[0].Path != "/pics/a&b=1?size=big-a%26b%3D1" {
		t.Fatalf("a tool command makes one call with parameters from its input: %+v %+v", got, svc.requests())
	}
	var files []struct{ Type string }
	e.must("deck.files_list", map[string]any{"process": got.ID}, &files)
	if len(files) != 1 || files[0].Type != "image/png" {
		t.Fatalf("a binary answer is a file of the process: %+v", files)
	}

	id := e.start("pics", "send", map[string]any{"text": "hi"})
	e.waitState(id, "waiting_answer")
	if len(svc.requests()) != 1 {
		t.Fatal("an external tool behind an internal command waits for the owner")
	}
	e.answer(e.waitAsk().ID, "allow")
	sent := e.waitState(id, "done", "error")
	if sent.State != "done" || !strings.Contains(string(sent.Result), `"id":"n1"`) || svc.requests()[1].Body != `{"text":"hi"}` {
		t.Fatalf("after yes the tool command ends with what the call gave: %+v %+v", sent, svc.requests())
	}
	id = e.start("pics", "send", map[string]any{"text": "no"})
	e.waitState(id, "waiting_answer")
	e.answer(e.waitAsk().ID, "deny")
	if got := e.waitState(id, "error", "done"); got.State != "error" || !strings.Contains(got.Error, "rejected") || len(svc.requests()) != 2 {
		t.Fatalf("a rejected tool command ends with the refusal: %+v", got)
	}
}

func TestAllowAlwaysAddsTheToolToTheProjectList(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, fmt.Sprintf(`{"id": "n%d"}`, n)) })
	e := newEnv(t, nil)
	e.createProject("pics", toolManifest(svc.URL, ""))
	id := e.start("pics", "send", map[string]any{"text": "one"})
	e.waitState(id, "waiting_answer")
	e.answer(e.waitAsk().ID, "allow_always")
	e.waitState(id, "done")
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "pics"}, &m)
	if !strings.Contains(m.Manifest, `"allowed_external"`) || !strings.Contains(m.Manifest, `"note"`) {
		t.Fatalf("allow always puts the tool into the allowed external list: %s", m.Manifest)
	}
	if got := e.waitState(e.start("pics", "send", map[string]any{"text": "two"}), "done", "waiting_answer"); got.State != "done" || len(svc.requests()) != 2 {
		t.Fatalf("an allowed tool runs at once: %+v", got)
	}
}

func TestReversibleWaitsInSuggestAndRunsInAuto(t *testing.T) {
	outs := map[string]string{}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
			project := "suggest"
			if len(outs) > 0 {
				project = "auto"
			}
			out, _ := c.Tool(ctx, project+"__merge", map[string]any{})
			outs[project] = out
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	s := e.createProject("suggest", toolManifest("http://127.0.0.1:1", ""))
	e.waitState(e.run(s, "work"), "done", "waiting_answer")
	a := e.createProject("auto", toolManifest("http://127.0.0.1:1", `, "autonomy": "auto"`))
	e.waitState(e.run(a, "work"), "done", "waiting_answer")
	if !strings.Contains(outs["suggest"], `"waiting"`) {
		t.Fatalf("reversible in suggest waits for the owner: %s", outs["suggest"])
	}
	if !strings.Contains(outs["auto"], `"process"`) {
		t.Fatalf("reversible in auto runs at once: %s", outs["auto"])
	}
}

func TestHTTPToolTemplatesStayOnTheirHost(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range []struct{ tool, want string }{
		{`{"name": "t", "description": "d", "method": "GET", "url": "https://{host}/x", "input": {"type": "object", "properties": {"host": {"type": "string"}}}}`, "host"},
		{`{"name": "t", "description": "d", "method": "GET", "url": "https://a.com/x?key=$secret.k"}`, "only in headers"},
		{`{"name": "t", "description": "d", "method": "GET", "url": "https://a.com/{id}"}`, "{id} is not an input field"},
		{`{"name": "t", "description": "d", "method": "GET", "url": "ftp://a.com/x"}`, "absolute http"},
	} {
		code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": `{"version": 4, "tools": {"http": [` + c.tool + `]}}`})
		if code != 422 || !strings.Contains(string(out), c.want) {
			t.Fatalf("%s: want 422 with %q, got %d %s", c.tool, c.want, code, out)
		}
	}
	unfit := `{"version": 4, "commands": [{"name": "c", "title": "C", "description": "d", "class": "external",
    "input": {"type": "object", "properties": {"other": {"type": "string"}}}, "executor": {"type": "tool", "tool": "t"}}],
  "tools": {"http": [{"name": "t", "description": "d", "method": "POST", "url": "https://a.com/x",
    "input": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]}}]}}`
	if code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": unfit}); code != 422 ||
		!strings.Contains(string(out), `"other" is not an input field of tool`) || !strings.Contains(string(out), `requires input field "id"`) {
		t.Fatalf("a tool command's input must fit its tool: %d %s", code, out)
	}
	e.must("deck.secret_set", map[string]any{"name": "k", "value": "v"}, nil)
	var names []string
	e.must("deck.secrets_list", nil, &names)
	if len(names) != 1 || names[0] != "k" {
		t.Fatalf("secrets_list gives names only: %v", names)
	}
}

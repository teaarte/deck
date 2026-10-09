package scenarios

import (
	"fmt"
	"strings"
	"testing"
)

// jsonToolManifest is a project with an HTTP tool whose input field messages is JSON text, put into the body as
// the list it holds, and one that sends its whole input as the body.
func jsonToolManifest(url string) string {
	return fmt.Sprintf(`{"version": 10,
  "commands": [{"name": "ask", "title": "Ask", "description": "Asks a model.", "class": "internal",
    "input": {"type": "object", "properties": {"messages": {"type": "string", "format": "json"}}},
    "executor": {"type": "script", "script": "main"}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}],
  "tools": {"http": [
    {"name": "chat", "title": "Chat", "description": "Chat completions.", "class": "internal", "method": "POST", "url": "%[1]s/v1/chat/completions",
     "input": {"type": "object", "properties": {"model": {"type": "string"}, "messages": {"type": "string", "format": "json"}}, "required": ["model", "messages"]},
     "body": {"model": "{model}", "messages": "{messages}", "modalities": ["image", "text"], "note": "for {model}"}},
    {"name": "plain", "title": "Plain", "description": "Posts its input.", "class": "internal", "method": "POST", "url": "%[1]s/v1/plain",
     "input": {"type": "object", "properties": {"options": {"type": "string", "format": "json"}}}}]}}`, url)
}

// TestAJSONFieldGoesIntoTheBodyOfAToolAsItsValue: a field of format json is text for the caller (an App Intents
// string) and the value it holds in the body of the request, whether the caller sent the text or the value.
func TestAJSONFieldGoesIntoTheBodyOfAToolAsItsValue(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", chatText("hi") },
		"/v1/plain":            func(int, map[string]any) (int, string, string) { return 200, "application/json", `{"ok": true}` },
	})
	e := newEnv(t, nil)
	e.createProject("web", jsonToolManifest(api.URL))
	e.saveScript("web", "scripts/main.py", `import json, os, sys, urllib.request
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/web.chat", data=json.dumps({"model": "m", "messages": inp["messages"]}).encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
with urllib.request.urlopen(req, timeout=60) as r:
    print(json.dumps({"status": r.status}))
`)
	messages := `[{"role": "user", "content": "A painter at a desk"}]`
	if got := e.waitScript(e.start("web", "ask", map[string]any{"messages": messages}), "done", "error"); got.State != "done" {
		t.Fatalf("the script calls the tool with the JSON text, as scripts send it: %+v", got)
	}
	_, tok := e.token("agent", "web.*")
	if code, out := e.callWith(tok, "web.chat", map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": "A painter at a desk"}}}); code != 200 {
		t.Fatalf("a caller may send the value itself: %d %s", code, out)
	}
	for i, c := range api.calls() {
		list, ok := c.Body["messages"].([]any)
		if !ok || len(list) != 1 || list[0].(map[string]any)["content"] != "A painter at a desk" || c.Body["note"] != "for m" {
			t.Fatalf("call %d: the json field is the list in the body, the other fields as before: %+v", i, c.Body)
		}
	}
	e.must("web.plain", map[string]any{"options": `{"voice": "onyx"}`}, nil)
	if last := api.calls()[2]; last.Body["options"].(map[string]any)["voice"] != "onyx" {
		t.Fatalf("a tool that posts its input posts the json field as its value: %+v", last.Body)
	}
	if code, out := e.call("web.chat", map[string]any{"model": "m", "messages": "[not json"}); code != 422 || !strings.Contains(string(out), "messages") {
		t.Fatalf("text that is not JSON is refused for its field: %d %s", code, out)
	}
}

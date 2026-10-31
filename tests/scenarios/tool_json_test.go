package scenarios

import (
	"deck2/tests/harness"
	"fmt"
	"net"
	"strings"
	"testing"
)

// jsonToolManifest is a project with an HTTP tool whose input field messages is JSON text, put into the body as
// the list it holds, and one that sends its whole input as the body.
func jsonToolManifest(url string) string {
	return fmt.Sprintf(`{"version": 13,
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
	e := harness.New(t)
	e.Project("web", jsonToolManifest(api.URL))
	e.SaveScript("web", "scripts/main.py", `import json, os, sys, urllib.request
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/web.chat", data=json.dumps({"model": "m", "messages": inp["messages"]}).encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
with urllib.request.urlopen(req, timeout=60) as r:
    print(json.dumps({"status": r.status}))
`)
	messages := `[{"role": "user", "content": "A painter at a desk"}]`
	if got := e.WaitState(e.Start("web", "ask", map[string]any{"messages": messages}), "done", "error"); got.State != "done" {
		t.Fatalf("the script calls the tool with the JSON text, as scripts send it: %+v", got)
	}
	_, tok := e.Token("agent", "web.*")
	if code, out := e.As(tok).Call("web.chat", map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": "A painter at a desk"}}}); code != 200 {
		t.Fatalf("a caller may send the value itself: %d %s", code, out)
	}
	for i, c := range api.calls() {
		list, ok := c.Body["messages"].([]any)
		if !ok || len(list) != 1 || list[0].(map[string]any)["content"] != "A painter at a desk" || c.Body["note"] != "for m" {
			t.Fatalf("call %d: the json field is the list in the body, the other fields as before: %+v", i, c.Body)
		}
	}
	e.Must("web.plain", map[string]any{"options": `{"voice": "onyx"}`}, nil)
	if last := api.calls()[2]; last.Body["options"].(map[string]any)["voice"] != "onyx" {
		t.Fatalf("a tool that posts its input posts the json field as its value: %+v", last.Body)
	}
	if code, out := e.Call("web.chat", map[string]any{"model": "m", "messages": "[not json"}); code != 422 || !strings.Contains(string(out), "messages") {
		t.Fatalf("text that is not JSON is refused for its field: %d %s", code, out)
	}
}

// TestAToolGoesOnlyToTheHostItsManifestWrites: a field in the host of a tool's URL is refused on save; a call goes
// to the addresses its host resolves to once, a name of the owner's machine only when the domains name its addresses.
func TestAToolGoesOnlyToTheHostItsManifestWrites(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/plain": func(int, map[string]any) (int, string, string) { return 200, "application/json", `{"ok": true}` },
	})
	e := harness.New(t)
	code, out := e.Call("deck.project_create", map[string]any{"name": "bad", "manifest": jsonToolManifest("http://{host}")})
	if code != 422 || !strings.Contains(string(out), "cannot fill the host") {
		t.Fatalf("a field in the host is refused on save: %d %s", code, out)
	}
	e.Project("named", jsonToolManifest(strings.Replace(api.URL, "127.0.0.1", "localhost", 1)))
	if code, out := e.Call("named.plain", map[string]any{}); code != 403 || !strings.Contains(string(out), "not a public address") {
		t.Fatalf("a name of the owner's machine is refused: %d %s", code, out)
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("the refused call reached nothing: %d", n)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(api.URL, "http://"))
	// The addresses of the owner's machine are reached by name only once the domains name them.
	listed := strings.Replace(jsonToolManifest("http://localhost:"+port), `"tools": {`, `"domains": {"read": ["127.0.0.1", "::1"]}, "tools": {`, 1)
	e.Project("listed", listed)
	e.Must("listed.plain", map[string]any{}, nil)
	e.Project("nowhere", jsonToolManifest("http://nowhere.invalid"))
	if code, out := e.Call("nowhere.plain", map[string]any{}); code != 502 || !strings.Contains(string(out), "does not resolve") {
		t.Fatalf("a name that does not resolve is the network's: %d %s", code, out)
	}
	e.Project("web", jsonToolManifest(api.URL))
	e.Must("web.plain", map[string]any{}, nil)
}

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// asksModule is a module whose script posts three notes through deck, each waiting for the owner, and whose agent
// posts one.
func asksModule(url string) string {
	return harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "run", "title": "Run", "description": "Posts three notes, or one.",
    "input": {"type": "object", "properties": {"one": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}},
    {"name": "work", "title": "Work", "description": "Posts a note.", "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Post."}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}}]},
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "actions", "title": "Post", "actions": [{"label": "Post one", "command": "run", "params": {"one": "1"}}]}]}],
  "menu": ["main"],
  "rights": [{"right": "send", "what": ["post"], "why": "posts notes to the service"}]}`, url))
}

// TestRealBrowserTheRequestsOfOneWorkAreOneCard: on Today a work's requests are one card, answered each or all; each
// says why it waits with a way to Rights, a lone one offers always, a process says why; on a desk and a phone.
func TestRealBrowserTheRequestsOfOneWorkAreOneCard(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "notes__post", map[string]any{"id": "agent", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	e.Project("notes", asksModule(svc.URL))
	e.SaveScript("notes", "scripts/main.py", harness.PythonLib+`
one = json.load(sys.stdin).get("one")
got = [deck("notes.post", {"id": i + (one or ""), "text": "hi " + i})[0] for i in (["p"] if one else ["a", "b", "c"])]
print(json.dumps({"got": got}))
`)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	card := `document.querySelector('[data-focus-card]')`
	button := func(in, label string) string {
		return `[...(` + in + `?.querySelectorAll('button') ?? [])].find((x) => x.innerText.trim().startsWith('` + label + `'))`
	}
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		posts := len(svc.Requests())
		e.WaitState(e.Start("notes", "run", nil), "done")
		b.Open(e, "#/deck/cockpit")
		b.until(size.name+": one card for the three requests of the work, each with why it waits and its buttons",
			card+`?.querySelectorAll('[data-part]').length === 3 && `+card+`.innerText.includes('This work asks for 3 things') &&
			[...`+card+`.querySelectorAll('[data-part]')].every((p) => p.innerText.includes('Waits for your answer') && p.querySelector('button'))`)
		b.shot("t9-one-card-" + size.name)
		part := card + `.querySelectorAll('[data-part]')[0]`
		b.eval(`(` + button(part, "Allow") + `.click(), '')`)
		e.Until(size.name+": the part allowed goes out", func() bool { return len(svc.Requests()) == posts+1 })
		b.until(size.name+": two parts left", card+`?.querySelectorAll('[data-part]').length === 2`)
		b.eval(`(` + button(card, "Allow all") + `.click(), '')`)
		e.Until(size.name+": all go out at once", func() bool { return len(svc.Requests()) == posts+3 })
		b.until(size.name+": all decided", `document.querySelector('[data-focus-done]')`)

		if code, out := e.As(tok).Call("notes.post", map[string]any{"id": "lone-" + size.name, "text": "hi"}); code >= 300 || !strings.Contains(string(out), `"waiting"`) {
			t.Fatalf("%s: a lone post waits: %d %s", size.name, code, out)
		}
		b.until(size.name+": a lone request offers always on Today and says why it waits", button(card, "Always let notes")+
			` && `+card+`.innerText.includes('Waits for your answer') && `+button(card, "Rights"))
		b.shot("t9-always-" + size.name)
		b.eval(`(` + button(card, "Rights") + `.click(), '')`)
		b.until(size.name+": Rights of the module, where it is decided", `location.hash.includes('settings') && location.hash.includes('Rights') && document.querySelector('[data-group="notes"]')`)
		for _, a := range e.Asks() {
			e.Answer(a.ID, "deny:not now")
		}

		b.Open(e, "#/p/notes/main")
		b.until(size.name+": the button of the module", button(`document`, "Post one"))
		b.eval(`(` + button(`document`, "Post one") + `.click(), '')`)
		b.until(size.name+": the request of the run over the screen offers always and says why it waits",
			`document.querySelector('[data-press-answer="allow_always"]')?.innerText.includes('Always let notes') && document.querySelector('[data-press-waits]')`)
		b.shot("t9-press-always-" + size.name)
		b.eval(`(document.querySelector('[data-press-answer="later"]').click(), '')`)
		for _, a := range e.Asks() {
			e.Answer(a.ID, "deny:not now")
		}

		id := e.Start("notes", "work", nil)
		e.WaitAsk()
		b.Open(e, "#/deck/process/"+fmt.Sprint(id))
		b.until(size.name+": the page of the process says why it waits, with a way to Rights", `document.body.innerText.includes('Why it waits') &&
			document.body.innerText.includes('Waits for your answer') && `+button(`document`, "Rights"))
		b.shot("t9-process-waits-" + size.name)
		e.Must("deck.process_stop", map[string]any{"process": id}, nil)
		e.WaitState(id, "stopped", "done", "error")
		e.Until(size.name+": nothing waits", func() bool { return len(e.Asks()) == 0 })
	}
}

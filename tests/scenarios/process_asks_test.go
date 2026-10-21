package scenarios

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// pressScript makes the deck call of its input and prints deck's answer as its result; with n it only names ask n
// as the one its work waits for.
const pressScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
if inp.get("n"):
    print(json.dumps({"waiting": inp["n"]}))
    sys.exit(0)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + inp["method"], data=inp["form"].encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
try:
    body = urllib.request.urlopen(req, timeout=30).read().decode()
except urllib.error.HTTPError as e:
    body = e.read().decode()
print(body if body.strip() else "{}")
`

type pressAsk struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	What  string `json:"what"`
	Where string `json:"where"`
}

// pressAsks are the calls a run the owner started waits to make, as the button reads them.
func (e *Env) pressAsks(process int64) []pressAsk {
	e.T.Helper()
	var out []pressAsk
	e.must("deck.process_asks", map[string]any{"process": process}, &out)
	return out
}

// pressRun starts the script of project as a button of the owner does, with the deck call of method and form.
func (e *Env) pressRun(project, method string, form map[string]any) int64 {
	e.T.Helper()
	raw, _ := json.Marshal(form)
	return e.start(project, "main", map[string]any{"method": method, "form": string(raw)})
}

// TestTheOwnersPressAnswersTheCallItsRunWaitsFor: a call a run of the owner's button waits to make comes back to
// the button with what it was and what it becomes; yes there applies it and nothing waits in the cockpit.
func TestTheOwnersPressAnswersTheCallItsRunWaitsFor(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "gemma", "kind": "text", "call_usd": 0.02}, nil)
	e.createProject("shorts", plainManifest)
	e.createProject("picker", scriptManifest("main", "", ""))
	e.saveScript("picker", "scripts/main.py", pressScript)
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)

	run := e.pressRun("picker", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	asks := e.pressAsks(run)
	if len(asks) != 1 || asks[0].Kind != "call" || asks[0].What != "Assign models" || asks[0].Where != "text of shorts: openrouter/gemma → openrouter/qwen" {
		t.Fatalf("the button gets the call its run waits for, before and after: %+v", asks)
	}
	if got := e.pressAsks(e.start("picker", "main", map[string]any{"n": asks[0].ID})); len(got) != 1 || got[0].ID != asks[0].ID {
		t.Fatalf("a run that names the ask it waits for gives the same ask to answer, not a copy: %+v", got)
	}
	e.answer(asks[0].ID, "allow")
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("yes at the button applies the call: %v", got)
	}
	var left []struct{ Kind string }
	e.must("deck.asks_list", nil, &left)
	if len(left) != 0 {
		t.Fatalf("nothing waits in the cockpit after the yes at the button: %+v", left)
	}

	if got := e.pressAsks(e.pressRun("picker", "deck.models_list", map[string]any{})); len(got) != 0 {
		t.Fatalf("a run that waits for nothing gives the button nothing once it ends: %+v", got)
	}
	add := e.pressRun("picker", "deck.model_set", map[string]any{"connection": "openrouter", "model": "llama", "kind": "text"})
	if got := e.pressAsks(add); len(got) != 1 || got[0].Where != "openrouter/llama: not in deck → text" {
		t.Fatalf("a model to add says what it becomes: %+v", got)
	}

	e.createProject("other", scriptManifest("main", "", ""))
	e.saveScript("other", "scripts/main.py", pressScript)
	theirs := e.pressAsks(add)[0].ID
	if got := e.pressAsks(e.start("other", "main", map[string]any{"n": theirs})); len(got) != 0 {
		t.Fatalf("a run names no ask of another project to the button: %+v", got)
	}
	if code, out := e.call("deck.process_asks", map[string]any{"process": 99999}); code != 404 || !strings.Contains(string(out), "no process") {
		t.Fatalf("no asks of a process that is not there: %d %s", code, out)
	}
}

// TestRealBrowserPressAnswersItsAsk: the call the run of Apply waits for comes up over the screen, yes applies it:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserPressAnswersItsAsk -v.
func TestRealBrowserPressAnswersItsAsk(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "gemma", "kind": "text", "call_usd": 0.02}, nil)
	e.createProject("shorts", plainManifest)
	form, _ := json.Marshal(map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	params, _ := json.Marshal(map[string]string{"method": "deck.assignment_set", "form": string(form)})
	e.createProject("picker", scriptManifest("main", "", `, "screens": [{"name": "main", "title": "Proposals", "blocks": [
    {"type": "actions", "title": "Proposal", "actions": [{"label": "Apply", "command": "main", "params": `+string(params)+`}]}]}], "menu": ["main"]`))
	e.saveScript("picker", "scripts/main.py", pressScript)
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)

	b := newPlaywright(t)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
		b.until("the cockpit", `document.querySelector('[data-hero]')`)
		b.open(e.URL + "/?size=" + size.name + "#/p/picker/main")
		b.until("the Apply button", `[...document.querySelectorAll('button')].some((x) => x.innerText === 'Apply')`)
		b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText === 'Apply').click(), '')`)
		b.until("the call of the run over the screen", `document.querySelector('[data-press-where]')?.innerText.includes('text of shorts:') && document.querySelector('[data-press-where]').innerText.includes('→ openrouter/qwen')`)
		b.shot("press-ask-" + size.name)
		b.eval(`(document.querySelector('[data-press-answer="allow"]').click(), '')`)
		b.until("the window gone after yes", `!document.querySelector('[data-press-ask] [data-press-answer]')`)
		if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
			t.Fatalf("yes at the button applies the call on a %s: %v", size.name, got)
		}
		var left []struct{ Kind string }
		e.must("deck.asks_list", nil, &left)
		if len(left) != 0 {
			t.Fatalf("nothing waits in the cockpit on a %s: %+v", size.name, left)
		}
		e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)
	}
}

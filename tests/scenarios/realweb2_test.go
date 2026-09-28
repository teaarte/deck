package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// customProbe is the HTML of a custom block that tries every way out of its frame and reports what it saw
// through the one command of its buttons.
const customProbe = `<!doctype html><html><body><p id="out">waiting</p><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "probe", version: "1" });
let got;
const data = new Promise((ok) => { app.ontoolresult = (r) => { got = r.structuredContent; ok(); }; });
await app.connect();
const tryFetch = (url, init) => fetch(url, init).then(() => "open", () => "blocked");
const direct = await tryFetch(%q + "/api/commands/web.mark", { method: "POST", headers: { Authorization: "Bearer %s" }, body: "{}" });
const self = await tryFetch("/sandbox.html");
let top = "open";
try { void window.top.location.href; } catch { top = "blocked"; }
await data;
const other = await app.callServerTool({ name: "web.score", arguments: { title: "sneaky" } });
const report = { direct, self, top, items: got.items.map((i) => i.title), otherRefused: other.isError === true };
const res = await app.callServerTool({ name: "web.mark", arguments: { text: JSON.stringify(report) } });
document.getElementById("out").textContent = JSON.stringify(res.structuredContent);
await app.callServerTool({ name: "web.tag", arguments: { tag: "b" } });
await app.callServerTool({ name: "web.publish", arguments: {} });
try { const a = parent.document.createElement("a"); a.href = %q + "/parent"; parent.document.body.appendChild(a); a.click(); } catch {}
setTimeout(() => { location.href = %q + "/self"; }, 300);
</script></body></html>`

// TestRealBrowserWebClientII drives the board, forms, a custom block, sharing and the schedule in the real
// browser of Playwright MCP: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserWebClientII(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	evil := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { w.Write([]byte("<p>out</p>")) })
	e := newEnv(t, boardWebAgent())
	e.createProject("web", boardWebManifest)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom",
		"text": fmt.Sprintf(customProbe, e.URL, token, evil.URL, evil.URL)}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "gamma", "score": 1, "source": "seed", "extra": "keep me"})}, nil)
	a := e.card(map[string]any{"project": "web", "title": "Write the plan"})
	e.card(map[string]any{"project": "web", "title": "Ship it"})

	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the board", `document.querySelectorAll('[data-card]').length === 2`)

	marks := func() []proc {
		var ps []struct {
			ID      int64
			Command string
		}
		e.must("deck.processes_list", map[string]any{"project": "web"}, &ps)
		var out []proc
		for _, x := range ps {
			if x.Command == "mark" {
				out = append(out, e.process(x.ID))
			}
		}
		return out
	}
	e.waitFor("the custom block to report through the bridge", func() bool { ms := marks(); return len(ms) > 0 && ms[0].State == "done" })
	e.settle()
	ms := marks()
	var input struct{ Seen string }
	_ = json.Unmarshal(ms[0].Result, &input)
	var report struct {
		Direct, Self, Top string
		Items             []string
		OtherRefused      bool
	}
	if err := json.Unmarshal([]byte(input.Seen), &report); err != nil {
		t.Fatalf("the block reports through the bridge: %s %v", ms[0].Result, err)
	}
	if len(ms) != 1 || report.Direct != "blocked" || report.Self != "blocked" || report.Top != "blocked" ||
		len(report.Items) != 1 || report.Items[0] != "gamma" || !report.OtherRefused {
		t.Fatalf("a custom block gets its data and calls its command only through the bridge: %d marks, %+v", len(ms), report)
	}
	if e.countOf("score") != 0 {
		t.Fatal("a command outside the buttons of the block does not run")
	}
	e.waitFor("the tag the block picked by its params", func() bool { return e.countOf("tag") == 1 })
	for _, x := range e.processes() {
		if x.Command == "tag" {
			if got := e.waitState(x.ID, "done"); !strings.Contains(string(got.Result), `"seen":"b"`) {
				t.Fatalf("a call picks the button whose fixed params it names: %s", got.Result)
			}
		}
	}
	b.until("the owner asked before an external command of the block", `document.querySelector('[data-custom-ask]')?.innerText.includes('web.publish')`)
	if e.countOf("publish") != 0 {
		t.Fatal("an external command of a custom block waits for the owner")
	}
	b.eval(`(document.querySelector('[data-custom-allow]').click(), '')`)
	e.waitFor("the published run", func() bool { return e.countOf("publish") == 1 })
	time.Sleep(2 * time.Second)
	if n := len(evil.requests()); n != 0 {
		t.Fatalf("a custom block cannot move its frames to a page on the network: %d requests", n)
	}
	b.shot("web2-main")

	handle := func(id int64) string { return fmt.Sprintf(`document.querySelector('[data-handle="%d"]')`, id) }
	b.drag(handle(a.ID), `document.querySelector('[data-lane="doing"]')`)
	e.waitFor("the moved card", func() bool {
		var c cardV
		e.must("deck.card_get", map[string]any{"card": a.ID}, &c)
		return c.Lane == "doing"
	})
	b.until("the card in its new lane", `document.querySelector('[data-lane="doing"] [data-card="`+fmt.Sprint(a.ID)+`"]')`)

	b.type_(`document.querySelector('[data-new-card]')`, "From the web")
	b.eval(`(document.querySelector('[data-new-card]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })), '')`)
	b.until("the new card in the inbox", `[...document.querySelectorAll('[data-lane="backlog"] [data-card]')].some((c) => c.innerText.includes('From the web'))`)

	b.eval(`([...document.querySelectorAll('[data-card]')].find((c) => c.innerText.includes('Ship it')).querySelector('button').click(), '')`)
	b.until("the card editor", `[...document.querySelectorAll('button')].some((x) => x.innerText === 'Delete')`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText === 'Delete').click(), '')`)
	b.until("the undo of the delete", `document.querySelector('[data-undo]')`)
	b.until("the deleted card to leave", `![...document.querySelectorAll('[data-card]')].some((c) => c.innerText.includes('Ship it'))`)
	b.eval(`(document.querySelector('[data-undo]').click(), '')`)
	b.until("the restored card", `[...document.querySelectorAll('[data-card]')].some((c) => c.innerText.includes('Ship it'))`)
	var shipped cardV
	for _, l := range e.board("web", false).Lanes {
		for _, c := range l.Cards {
			if c.Title == "Ship it" {
				shipped = c
			}
		}
	}
	e.must("deck.card_delete", map[string]any{"card": shipped.ID}, nil)
	b.until("the card deleted on another device in the list of deleted ones", `document.querySelector('[data-deleted="`+fmt.Sprint(shipped.ID)+`"]')`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.startsWith('Deleted in the last day')).click(), '')`)
	b.eval(`(document.querySelector('[data-deleted="` + fmt.Sprint(shipped.ID) + `"] button').click(), '')`)
	b.until("the card restored from the list", `[...document.querySelectorAll('[data-card]')].some((c) => c.innerText.includes('Ship it'))`)

	b.type_(`document.querySelector('[data-form="web.score"] input[data-field="title"]')`, "alpha")
	b.file(`document.querySelector('[data-form="web.score"] input[type=file]')`, "scan.txt", "a scanned page")
	b.eval(`(document.querySelector('[data-form="web.score"] [data-submit]').click(), '')`)
	b.until("the result drawn as a table by the result schema",
		`document.querySelector('[data-outcome="done"] [data-result="table"]')?.innerText.includes('beta')`)
	b.until("the form ready for the next run", `!document.querySelector('[data-form="web.score"] [data-submit]').hasAttribute('data-loading')`)
	scored := int64(0)
	for _, x := range e.processes() {
		if x.Command == "score" {
			scored = x.ID
		}
	}
	var files []struct{ Name string }
	e.must("deck.files_list", map[string]any{"process": scored}, &files)
	if len(files) != 1 || files[0].Name != "scan.txt" {
		t.Fatalf("the file of the form reaches the command: %+v", files)
	}
	b.eval(`(document.querySelector('[data-save]').click(), '')`)
	b.until("the kept result", `document.body.innerText.includes('Kept in notes')`)
	var notes []struct{ Title string }
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &notes)
	if len(notes) != 3 {
		t.Fatalf("the result is kept as records of the collection: %+v", notes)
	}

	b.until("the kept notes in the table", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Notes') && x.innerText.includes('beta'))`)
	b.pick(`[data-filter="source"]`, "seed")
	b.until("the filtered table", `(() => { const t = [...document.querySelectorAll('[data-block]')].find((x) => x.querySelector('[data-filter]')); return t.innerText.includes('gamma') && !t.innerText.includes('beta') })()`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText === 'Open').click(), '')`)
	b.until("the edit form of the record", `document.querySelector('[data-form="deck.record_update"] input[data-field="title"]')?.value === 'gamma'`)
	if got := b.eval(`String(document.querySelector('[data-form="deck.record_update"] input[data-field="source"]').disabled && document.querySelector('[data-form="deck.record_update"] input[data-field="title"]').disabled)`); got != "true" {
		t.Fatalf("fields that do not change, the key too, are read only: %s", got)
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "gamma", "score": 1, "source": "seed", "extra": "keep me", "note": "fresh"})}, nil)
	b.until("the fresh value in the open form", `document.querySelector('[data-form="deck.record_update"] input[data-field="note"]')?.value === 'fresh'`)
	b.type_(`document.querySelector('[data-form="deck.record_update"] input[data-field="score"]')`, "42")
	b.eval(`(document.querySelector('[data-form="deck.record_update"] [data-submit]').click(), '')`)
	b.until("the saved record", `document.querySelector('[data-outcome="done"]')`)
	var rec []struct {
		Data map[string]any
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &rec)
	edited := false
	for _, r := range rec {
		edited = edited || (r.Data["title"] == "gamma" && r.Data["score"] == float64(42) && r.Data["source"] == "seed" && r.Data["extra"] == "keep me" && r.Data["note"] == "fresh")
	}
	if !edited {
		t.Fatalf("the form saves the record, keeps the fields it does not show and the fresh values of those the owner left: %+v", rec)
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "gamma", "score": 7, "source": "seed", "extra": "keep me", "note": "fresh"})}, nil)
	b.until("the saved field following the record again", `document.querySelector('[data-form="deck.record_update"] input[data-field="score"]')?.value === '7'`)

	e.createProject("inbox", testManifest([]string{agentCommand("keep", `{"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}}`)}, nil))
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the service worker", `navigator.serviceWorker.controller`)
	b.eval(`(() => { const f = document.createElement('form'); f.method = 'post'; f.action = '/share'; f.enctype = 'multipart/form-data';
		const i = document.createElement('input'); i.type = 'file'; i.name = 'file'; const dt = new DataTransfer();
		dt.items.add(new File(['shared text'], 'shared.pdf', { type: 'application/pdf' })); i.files = dt.files; f.appendChild(i);
		document.body.appendChild(f); f.submit(); return '' })()`)
	b.until("the share screen", `location.hash === '#/deck/share' && document.querySelector('[data-pick]')`)
	b.pick(`[data-pick]`, "Score (web.score)")
	b.until("the shared file in the form", `document.querySelector('[data-form="web.score"]')?.innerText.includes('shared.pdf')`)
	b.type_(`document.querySelector('[data-form="web.score"] input[data-field="title"]')`, "shared")
	b.eval(`(document.querySelector('[data-form="web.score"] [data-submit]').click(), '')`)
	b.until("the shared file sent", `document.querySelector('[data-outcome="done"]')`)
	last := int64(0)
	for _, x := range e.processes() {
		if x.Command == "score" && x.ID > last {
			last = x.ID
		}
	}
	e.must("deck.files_list", map[string]any{"process": last}, &files)
	if len(files) != 1 || files[0].Name != "shared.pdf" {
		t.Fatalf("a shared file reaches the command the owner picked: %+v", files)
	}

	e.createProject("timed", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	b.open(e.URL + "/#/deck/schedule")
	b.until("the schedule", `document.body.innerText.includes('0 9 * * *')`)
	b.eval(`([...document.querySelectorAll('tr')].find((r) => r.innerText.includes('0 9 * * *')).querySelector('button').click(), '')`)
	b.until("the started run", `document.body.innerText.includes('Started process')`)
	e.waitFor("the run of the trigger", func() bool { return e.countOf("job") == 1 })
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.shot("web2-schedule-phone")
	b.open(e.URL + "/#/p/web/main")
	b.until("the board on a phone", `document.querySelectorAll('[data-card]').length === 3`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the page fits the width of a phone; the board scrolls inside its block: %s", got)
	}
	b.shot("web2-main-phone")
}

// center is the middle of the element an expression finds, in page coordinates.
func (b *playwright) center(el string) (float64, float64) {
	b.t.Helper()
	var at struct{ X, Y float64 }
	raw := b.eval(`(() => { const e = ` + el + `; e.scrollIntoView({ block: 'center' }); const r = e.getBoundingClientRect();
		return JSON.stringify({ x: r.x + r.width / 2, y: r.y + Math.min(r.height / 2, 30) }) })()`)
	if err := json.Unmarshal([]byte(raw), &at); err != nil {
		b.t.Fatalf("no element %s: %s", el, raw)
	}
	return at.X, at.Y
}

// drag moves the mouse with its button down from one element to another in steps, as a hand does.
func (b *playwright) drag(from, to string) {
	b.t.Helper()
	x0, y0 := b.center(from)
	b.call("browser_mouse_move_xy", map[string]any{"element": "handle", "x": x0, "y": y0})
	b.call("browser_mouse_down", map[string]any{})
	x1, y1 := b.center(to)
	for i := 1; i <= 8; i++ {
		f := float64(i) / 8
		b.call("browser_mouse_move_xy", map[string]any{"element": "lane", "x": x0 + (x1-x0)*f, "y": y0 + (y1-y0)*f})
	}
	b.call("browser_mouse_up", map[string]any{})
}

// type_ sets the value of an input the way typing does, so React sees it.
func (b *playwright) type_(el, text string) {
	b.t.Helper()
	b.eval(fmt.Sprintf(`(() => { const i = %s; i.focus(); const proto = i instanceof HTMLTextAreaElement ? HTMLTextAreaElement : HTMLInputElement;
		Object.getOwnPropertyDescriptor(proto.prototype, 'value').set.call(i, %q);
		i.dispatchEvent(new Event('input', { bubbles: true })); i.dispatchEvent(new Event('change', { bubbles: true })); return '' })()`, el, text))
}

// file puts a file into a file input as picking it does.
func (b *playwright) file(el, name, content string) {
	b.t.Helper()
	b.eval(fmt.Sprintf(`(() => { const i = %s; const dt = new DataTransfer(); dt.items.add(new File([%q], %q, { type: 'text/plain' }));
		i.files = dt.files; i.dispatchEvent(new Event('change', { bubbles: true })); return '' })()`, el, content, name))
}

// pick opens a select and clicks the option with the label in the list of that select.
func (b *playwright) pick(sel, label string) {
	b.t.Helper()
	b.eval(fmt.Sprintf(`(document.querySelector(%q).click(), '')`, sel))
	opt := fmt.Sprintf(`(() => { const id = document.querySelector(%q).getAttribute('aria-controls'); const box = id && document.getElementById(id);
		return [...(box || document).querySelectorAll('[role=option]')].find((o) => o.innerText.trim() === %q) })()`, sel, label)
	b.until("the option "+label, opt)
	b.eval(fmt.Sprintf(`(%s.click(), '')`, opt))
}

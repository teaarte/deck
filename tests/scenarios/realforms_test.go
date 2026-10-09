package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
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
const first = app.callServerTool({ name: "web.publish", arguments: {} });
const second = await app.callServerTool({ name: "web.publish", arguments: {} });
await app.callServerTool({ name: "web.tag", arguments: { tag: second.isError ? "a" : "b" } });
await first;
try { const a = parent.document.createElement("a"); a.href = %q + "/parent"; parent.document.body.appendChild(a); a.click(); } catch {}
setTimeout(() => { location.href = %q + "/self"; }, 300);
</script></body></html>`

// TestRealBrowserWebClientII drives forms, a custom block, sharing and the schedule in the real
// browser of Playwright MCP: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserWebClientII(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	evil := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { w.Write([]byte("<p>out</p>")) })
	e := newEnv(t, webNotesAgent())
	e.createProject("web", webNotes)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom",
		"text": fmt.Sprintf(customProbe, e.URL, token, evil.URL, evil.URL)}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "gamma", "score": 1, "source": "seed", "extra": "keep me"})}, nil)

	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the screen", `document.querySelector('[data-form="web.score"]')`)

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
	e.waitFor("the tags the block picked by their params", func() bool { return e.countOf("tag") == 2 })
	var seen []string
	for _, x := range e.processes() {
		if x.Command == "tag" {
			seen = append(seen, string(e.waitState(x.ID, "done").Result))
		}
	}
	if !slices.ContainsFunc(seen, func(r string) bool { return strings.Contains(r, `"seen":"b"`) }) ||
		!slices.ContainsFunc(seen, func(r string) bool { return strings.Contains(r, `"seen":"a"`) }) {
		t.Fatalf("a call picks the button whose fixed params it names, and a second call while one waits for the owner is refused: %v", seen)
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
	b.until("the schedule in words", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('timed · job') && r.innerText.includes('09:00') && !r.innerText.includes('0 9 * * *'))`)
	b.eval(`([...document.querySelectorAll('tr')].find((r) => r.innerText.includes('timed · job')).querySelector('[data-verb=run_now]').click(), '')`)
	e.waitFor("the run of the trigger", func() bool { return e.countOf("job") == 1 })
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.shot("web2-schedule-phone")
	b.open(e.URL + "/#/p/web/main")
	b.until("the screen on a phone", `document.querySelector('[data-form="web.score"]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the page fits the width of a phone: %s", got)
	}
	b.shot("web2-main-phone")
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

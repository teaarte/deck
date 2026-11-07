package browser

import (
	"testing"

	"deck2/tests/harness"
)

var polishManifest = harness.Manifest(`{"title": "Polish", "icon": "sparkles",
  "collections": [{"name": "jobs", "description": "Jobs", "key": "name", "display": "name", "schema": {"type": "object", "required": ["name"],
    "properties": {"name": {"type": "string"}, "status": {"type": "string"}}}}],
  "screens": [
    {"name": "main", "title": "Main", "blocks": [
      {"type": "columns", "blocks": [
        {"type": "stat", "title": "Jobs", "icon": "list", "source": {"collection": "jobs"}},
        {"type": "stat", "title": "Open", "icon": "circle", "source": {"collection": "jobs", "where": "status == 'open'"}},
        {"type": "stat", "title": "Done", "icon": "check", "source": {"collection": "jobs", "where": "status == 'done'"}}]},
      {"type": "list", "title": "Jobs", "source": {"collection": "jobs", "sort": "name"}, "item": {"title": "name", "text": "status"},
        "actions": [{"label": "Open", "primary": true, "screen": "job"}, {"label": "Close", "command": "deck.record_delete", "confirm": true}]},
      {"type": "document", "title": "Guide", "document": "docs/guide.md"}]},
    {"name": "job", "title": "Job", "record": "jobs", "blocks": [{"type": "card", "fields": [{"label": "Name", "bind": "name"}]}]}],
  "menu": ["main"]
}`)

// TestRealBrowserScreensReadCalmly: a module's screens stand under its mark and name; its numbers go two a line on a
// phone; a line opens by a press on it, with no Open button; a document hides the notes of its authors.
func TestRealBrowserScreensReadCalmly(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Project("polish", polishManifest)
	e.Must("deck.record_upsert", map[string]any{"project": "polish", "collection": "jobs", "records": harness.Jsonl(
		map[string]any{"name": "backup", "status": "open"}, map[string]any{"name": "mail", "status": "done"})}, nil)
	e.Must("deck.document_save", map[string]any{"project": "polish", "name": "docs/guide.md", "kind": "doc",
		"text": "# Guide\n\n<!-- To fill with the owner: who reads it -->\nRead me first."}, nil)
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/p/polish/main")
		b.until(size.name+": the lines of jobs", `document.querySelectorAll('[data-item]').length === 2`)
		if size.name == "desktop" {
			b.until("the mark and the name of the module beside its tabs", `document.querySelector('[data-module-head]')?.innerText.includes('Polish')`)
		} else {
			b.until("the numbers two a line on a phone", `(() => { const s = [...document.querySelectorAll('[data-stat-block]')].map((x) => x.getBoundingClientRect());
				return s.length === 3 && Math.abs(s[0].top - s[1].top) < 2 && s[2].top > s[0].bottom - 1 && s[2].width > s[0].width * 1.5 })()`)
		}
		b.until(size.name+": a line opens by a press on it, with no Open button", `(() => { const l = document.querySelector('[data-item][data-opens]');
			return l && ![...l.querySelectorAll('button')].some((x) => x.innerText.trim() === 'Open') })()`)
		b.until(size.name+": the document shows its file name, not the note of its authors", `(() => { const d = document.querySelector('[data-document]');
			return d && d.querySelector('[data-document-name]').innerText === 'guide.md' && d.innerText.includes('Read me first') && !d.innerText.includes('To fill with the owner') })()`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("polish-" + size.name)
	}
	b.eval(`([...document.querySelectorAll('[data-item][data-opens]')].find((x) => x.innerText.includes('backup')).click(), '')`)
	b.until("the line opened its job", `location.hash.includes('/p/polish/job/')`)
}

package scenarios

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealBrowserClientKeepsWhatItShows: a failed read leaves the screen, the live channel stays one, a screen change
// draws no old screen, a document save does not overwrite a newer revision: DECK_REAL_BROWSER=1 go test -run TestRealBrowser
func TestRealBrowserClientKeepsWhatItShows(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	svc := newService(t, nil)
	e := newEnv(t, nil)
	e.createProject("notes", webManifest(svc.URL))
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 7})}, nil)
	var d struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "notes", "name": "plan.md", "kind": "doc", "text": "# Plan"}, &d)

	front := newFront(t, e.URL)
	b := newPlaywright(t)
	b.open(front.URL + "/?token=" + token + "#/p/notes/main")
	b.until("the project screen", `document.body.innerText.includes('All notes')`)

	front.fail.Store(true)
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	b.until("the refusal of the read", `document.body.innerText.includes('the read failed in the test')`)
	if got := b.eval(`String(!!document.querySelector('[data-screen-error]') || !document.body.innerText.includes('All notes'))`); got != "false" {
		t.Fatal("a failed read of a drawn screen leaves it drawn")
	}
	front.fail.Store(false)

	b.eval(`(() => { window.__added = 0; new MutationObserver((ms) => ms.forEach((m) => m.addedNodes.forEach((n) => {
		if (n.nodeType === 1) window.__added += (n.matches('[data-block]') ? 1 : 0) + n.querySelectorAll('[data-block]').length }))).observe(document.body, { childList: true, subtree: true }); return '' })()`)
	b.eval(`(location.hash = '#/p/notes/document/` + fmt.Sprint(d.ID) + `', '')`)
	b.until("the document page", `document.querySelector('[data-edit]')`)
	time.Sleep(500 * time.Millisecond)
	if got := b.eval(`String(window.__added - document.querySelectorAll('[data-block]').length)`); got != "0" {
		t.Fatalf("a screen change does not draw the old screen again: %s blocks more", got)
	}

	b.eval(`(document.querySelector('[data-edit]').click(), '')`)
	e.must("deck.document_save", map[string]any{"project": "notes", "name": "plan.md", "kind": "doc", "text": "# Plan of the agent"}, nil)
	b.type_(`document.querySelector('[data-draft]')`, "# Plan of the owner")
	b.eval(`(document.querySelector('[data-save-document]').click(), '')`)
	b.until("the refusal of a save over a newer revision", `document.body.innerText.includes('revision 2 now')`)
	var got struct{ Text string }
	e.must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Text != "# Plan of the agent" {
		t.Fatalf("the owner's save does not overwrite the revision written meanwhile: %q", got.Text)
	}

	front.cut()
	b.until("the stale mark while the live connection is down", `document.querySelector('[data-stale]')`)
	e.waitFor("a refused reconnect", func() bool { return front.refused.Load() > 0 })
	b.eval(`(location.hash = '#/p/notes/main', '')`)
	front.heal()
	b.until("the project screen again", `document.body.innerText.includes('All notes')`)
	b.until("the stale mark to clear", `![...document.querySelectorAll('[data-stale]')].some((x) => x.offsetParent)`)
	time.Sleep(5 * time.Second)
	if n := front.streams.Load(); n != 1 {
		t.Fatalf("the page keeps one live connection after it comes back: %d", n)
	}
	if !strings.Contains(b.eval(`document.body.innerText`), "All notes") {
		t.Fatal("the screen is drawn")
	}
}

package scenarios

import (
	"os"
	"strings"
	"testing"
)

// customData is a custom block that tells the master how many items of its source it got.
const customData = `<!doctype html><html><body><p>waiting</p><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "data", version: "1" });
app.ontoolresult = (r) => app.callServerTool({ name: "web.mark", arguments: { text: "items " + r.structuredContent.items.length } });
await app.connect();
</script></body></html>`

// TestRealBrowserCustomBlockWorksInWebKit: in WebKit, as in Safari, a custom block loads the MCP Apps library from the
// second origin and gets the data of its source.
func TestRealBrowserCustomBlockWorksInWebKit(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := New(t, WithAgent(webNotesAgent()))
	e.Project("web", webNotesMark)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "a"})}, nil)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customData}, nil)
	b := newPlaywright(t, "webkit")
	b.Open(e, "#/p/web/main")
	e.Until("the block got its data", func() bool {
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, "items 1") {
				return true
			}
		}
		return false
	})
}

// TestRealBrowserCustomBlockSaysWhatTheBrowserRefused: a block whose script the CSP refuses shows why, not an empty
// frame.
func TestRealBrowserCustomBlockSaysWhatTheBrowserRefused(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := New(t, WithAgent(webNotesAgent()))
	e.Project("web", webNotesMark)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom",
		"text": `<!doctype html><html><body><script src="https://example.com/lib.js"></script></body></html>`}, nil)
	b := newPlaywright(t)
	b.Open(e, "#/p/web/main")
	b.until("the refusal beside the block", `document.querySelector('[data-custom-error]')?.innerText.includes('example.com')`)
}

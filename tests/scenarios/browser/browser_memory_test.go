package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserMemoryTab opens Settings → Memory: the picture of what feeds what in its frame and the cards of
// memory with the lesson of a module, on a desktop and on a phone.
func TestRealBrowserMemoryTab(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", "")}, nil))
	e.Must("deck.document_save", map[string]any{"name": "memory/likes-tea.md", "kind": "memory",
		"text": harness.Fact("likes-tea", "user", "The owner drinks tea")}, nil)
	e.Must("deck.document_save", map[string]any{"name": "memory/MEMORY.md", "kind": "memory", "text": "- [Tea](likes-tea.md) — mornings\n"}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/site-waits.md", "kind": "memory",
		"text": "---\nname: site-waits\ndescription: The site answers after a minute\ntype: lesson\nsource: process:1\ncommand: collect\n---\n\nWait a minute.\n"}, nil)

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.Open(e, "#/deck/settings?tab=Memory")
	b.until("the picture in its frame", `!!document.querySelector('[data-media="frame"]')?.srcdoc?.includes('data-node="sleep"')`)
	b.until("the card of a lesson", `[...document.querySelectorAll('[data-item]')].some((r) => r.innerText.includes('site-waits') && r.querySelector('[data-verb="delete"], [data-more]'))`)
	b.until("the card of sleep", `[...document.querySelectorAll('[data-item]')].some((r) => r.innerText.includes('Sleep') && r.innerText.includes('30 3'))`)
	b.shot("memory-desktop")
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the picture on a phone", `!!document.querySelector('[data-media="frame"]')`)
	b.shot("memory-phone")
}

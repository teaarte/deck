package browser

import (
	"net"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// lanAddress is an IPv4 address of this machine on its network, empty when it has none.
func lanAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
}

// TestRealBrowserCustomBlockOpensOnEveryAddress: deck on every address, by [::] (Chrome) or localhost: the custom block
// gets its data, the console has no word of the CSP or the sandbox; plain http from the network gets nothing.
func TestRealBrowserCustomBlockOpensOnEveryAddress(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()), harness.OnEveryAddress)
	e.Project("web", harness.WebNotesMark)
	e.Must("deck.record_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "a"})}, nil)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customData}, nil)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(e.URL, "http://"))
	hosts := []string{"[::]", "0.0.0.0", "localhost", "127.0.0.1"}
	marks := func() int {
		n := 0
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, "items 1") {
				n++
			}
		}
		return n
	}
	for _, kind := range []string{"chrome", "webkit"} {
		b := Open(t, kind)
		for _, host := range hosts {
			if kind == "webkit" && (host == "0.0.0.0" || host == "[::]") {
				continue // WebKit does not connect to 0.0.0.0 or [::] at all.
			}
			before := marks()
			b.OpenVia("http://"+host+":"+port, e, "#/p/web/main")
			e.Until(kind+" by "+host+" to give the block its data", func() bool { return marks() > before })
			if at := b.eval(`location.host`); strings.HasPrefix(at, "[") || strings.HasPrefix(at, "0.0.0.0") {
				t.Fatalf("%s by %s stays on %s, an address no CSP names", kind, host, at)
			}
			logs := b.call("browser_console_messages", map[string]any{})
			for _, word := range []string{"Content Security Policy", "frame-src", "escape its sandboxing", "sandbox"} {
				if strings.Contains(logs, word) {
					t.Fatalf("%s by %s: the console speaks of %q:\n%s", kind, host, word, logs)
				}
			}
		}
		if lan := lanAddress(); lan != "" {
			b.open("http://" + lan + ":" + port + "/")
			b.until(kind+" by the network to get deck's refusal", `document.body.innerText.includes('deck answers only at its own addresses')`)
		}
	}
}

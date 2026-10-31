package scenarios

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"deck2/internal/app"
)

var refOf = regexp.MustCompile(`Send.{0,4} \[ref=(e\d+)\]`)

// TestRealBrowser drives the real Playwright MCP server: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v
func TestRealBrowser(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	var pixel, leaks atomic.Int32
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { pixel.Add(1) }))
	t.Cleanup(router.Close)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/leak" {
			leaks.Add(1)
			_, _ = w.Write([]byte(`<title>Leak</title><img src="` + router.URL + `/pixel.png">`))
			return
		}
		if r.URL.Path == "/upload" {
			_, _ = w.Write([]byte(`<title>Upload</title><input type="file" aria-label="Pick" onchange="document.title='Got '+this.files[0].name">`))
			return
		}
		_, _ = w.Write([]byte(`<title>Form</title><button onclick="document.title='Sent'">Send</button>`))
	}))
	t.Cleanup(page.Close)
	ag := &seqAgent{files: []string{"cv.txt"}}
	e := New(t, WithAgent(ag.script), WithConfig(func(c *app.Config) { c.Browser = app.DefaultBrowser }))
	host := strings.TrimPrefix(page.URL, "http://")
	e.Project("web", browserManifest(`{"read": ["127.0.0.1"], "act": ["`+host+`"]}`))
	e.WaitState(e.Start("web", "browse", steps(nav(page.URL), step{"browser_snapshot", map[string]any{}})), "done")
	snap := ag.last(1)[0]
	m := refOf.FindStringSubmatch(snap.out)
	if snap.isErr || m == nil {
		t.Fatalf("the real browser reads the page: %+v", snap)
	}
	e.WaitState(e.Start("web", "browse", steps(click(m[1]), step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(2); got[0].isErr || !strings.Contains(got[1].out, "Sent") {
		t.Fatalf("a click on an act domain runs at once in the real browser: %+v", got)
	}
	e.WaitState(e.Start("web", "browse", steps(nav(page.URL+"/upload"), step{"browser_snapshot", map[string]any{}})), "done")
	pick := regexp.MustCompile(`Pick.{0,4} \[ref=([ef\d]+)\]`).FindStringSubmatch(ag.last(1)[0].out)
	if pick == nil {
		t.Fatalf("the upload page reads: %+v", ag.last(1))
	}
	e.WaitState(e.Start("web", "browse", steps(click(pick[1]), step{"browser_file_upload", map[string]any{"paths": []string{"results/cv.txt"}}},
		step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(3); got[1].isErr || !strings.Contains(got[2].out, "Got cv.txt") {
		t.Fatalf("the real browser uploads a file of the process: %+v", got)
	}
	second := httptest.NewServer(page.Config.Handler)
	t.Cleanup(second.Close)
	other := second.URL
	id := e.Start("web", "browse", steps(nav(other), step{"browser_snapshot", map[string]any{}}, click("e2")))
	e.WaitState(id, "waiting_answer")
	if got := ag.last(1)[0]; !strings.Contains(got.out, `"waiting"`) {
		t.Fatalf("a click on a read domain of the real browser waits for the owner: %+v", got)
	}
	e.Project("research", browserManifest(`{"read": ["*", "`+host+`"]}`))
	e.WaitState(e.Start("research", "browse", steps(nav("https://example.com/"), step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(2); got[0].isErr || !strings.Contains(got[1].out, "Example Domain") || strings.Contains(got[1].out, second.URL) {
		t.Fatalf("a project reading any site reads the internet in a browser of its own, without the signed-in tabs: %+v", got)
	}
	e.WaitState(e.Start("research", "browse", steps(nav(page.URL+"/leak"))), "done")
	if leaks.Load() != 0 || pixel.Load() != 0 {
		t.Fatalf("the browser of any site reaches no local address, even one its project names: %d page, %d pixel", leaks.Load(), pixel.Load())
	}
}

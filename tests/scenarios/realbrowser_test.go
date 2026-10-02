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
	var pixel atomic.Int32
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { pixel.Add(1) }))
	t.Cleanup(router.Close)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/leak" {
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
	e := newEnv(t, ag.script, func(c *app.Config) { c.Browser = app.DefaultBrowser })
	host := strings.TrimPrefix(page.URL, "http://")
	e.createProject("web", browserManifest(`{"read": ["localhost"], "act": ["`+host+`"]}`))
	e.waitState(e.start("web", "browse", steps(nav(page.URL), step{"browser_snapshot", map[string]any{}})), "done")
	snap := ag.last(1)[0]
	m := refOf.FindStringSubmatch(snap.out)
	if snap.isErr || m == nil {
		t.Fatalf("the real browser reads the page: %+v", snap)
	}
	e.waitState(e.start("web", "browse", steps(click(m[1]), step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(2); got[0].isErr || !strings.Contains(got[1].out, "Sent") {
		t.Fatalf("a click on an act domain runs at once in the real browser: %+v", got)
	}
	e.waitState(e.start("web", "browse", steps(nav(page.URL+"/upload"), step{"browser_snapshot", map[string]any{}})), "done")
	pick := regexp.MustCompile(`Pick.{0,4} \[ref=([ef\d]+)\]`).FindStringSubmatch(ag.last(1)[0].out)
	if pick == nil {
		t.Fatalf("the upload page reads: %+v", ag.last(1))
	}
	e.waitState(e.start("web", "browse", steps(click(pick[1]), step{"browser_file_upload", map[string]any{"paths": []string{"results/cv.txt"}}},
		step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(3); got[1].isErr || !strings.Contains(got[2].out, "Got cv.txt") {
		t.Fatalf("the real browser uploads a file of the process: %+v", got)
	}
	other := strings.Replace(page.URL, "127.0.0.1", "localhost", 1)
	id := e.start("web", "browse", steps(nav(other), step{"browser_snapshot", map[string]any{}}, click("e2")))
	e.waitState(id, "waiting_answer")
	if got := ag.last(1)[0]; !strings.Contains(got.out, `"waiting"`) {
		t.Fatalf("a click on a read domain of the real browser waits for the owner: %+v", got)
	}
	e.createProject("research", browserManifest(`{"read": ["*", "`+host+`"]}`))
	e.waitState(e.start("research", "browse", steps(nav(page.URL), step{"browser_tabs", map[string]any{"action": "list"}})), "done")
	if got := ag.last(2); got[0].isErr || !strings.Contains(got[1].out, "Form") || strings.Contains(got[1].out, "localhost") {
		t.Fatalf("a project reading any site gets a real browser of its own without the signed-in tabs: %+v", got)
	}
	e.waitState(e.start("research", "browse", steps(nav(page.URL+"/leak"), step{"browser_snapshot", map[string]any{}})), "done")
	if got := ag.last(1)[0]; got.isErr || pixel.Load() != 0 {
		t.Fatalf("a page of any site reaches no local address it was not given: %+v, %d hits", got, pixel.Load())
	}
	e.waitState(e.start("research", "browse", steps(nav("https://example.com/"), step{"browser_snapshot", map[string]any{}})), "done")
	if got := ag.last(1)[0]; got.isErr || !strings.Contains(got.out, "Example Domain") {
		t.Fatalf("a project reading any site reads the internet through deck's proxy: %+v", got)
	}
}

package box

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestTheMasterReadsThePublicWebFromItsBox: the master as deck ships it reaches any public host from its box (200, or
// 502 on a machine offline, never the refusal of its rights), and no address of the local network.
func TestTheMasterReadsThePublicWebFromItsBox(t *testing.T) {
	t.Parallel()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "a page of the network at home") }))
	t.Cleanup(local.Close)
	f := harness.NewACP(t)
	code := func(url string) string { return `curl -s -m 20 -o /dev/null -w '%{http_code}' ` + url + `; echo` }
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "`+code("http://example.com/")+`; `+code(local.URL)+`"}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	e.UseModels("master.review", "acp")
	if got := e.WaitState(e.Start("master", "review", nil), "done", "error"); got.State != "done" {
		t.Fatalf("the review of the master runs: %+v", got)
	}
	shell := f.Call(4).Shell
	if len(shell) != 1 {
		t.Fatalf("the master ran its shell once: %+v", shell)
	}
	out := strings.Fields(shell[0]["out"])
	if len(out) != 2 || out[0] != "200" && out[0] != "502" || out[1] != "403" {
		t.Fatalf("the master reaches a public page and no page of the local network: %q", shell[0]["out"])
	}
}

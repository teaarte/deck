package box

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
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

// TestTheMastersTurnSeesTheFilesOfAMessage: a picture the owner attaches to a message is in the master's box at the
// path deck names in the turn's prompt, in a folder the turn only reads.
func TestTheMastersTurnSeesTheFilesOfAMessage(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	e.UseModels("master.reply", "acp")
	shot := "\x89PNG\r\n\x1a\n a screenshot"
	id := e.Upload("master", "image.png", "image/png", shot)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		fmt.Sprintf(`{"shell": "wc -c < {{cwd}}/../input/%d-image.png 2>&1"}`, id),
		fmt.Sprintf(`{"shell": "(echo planted > {{cwd}}/../input/%d-other.png || rm -f {{cwd}}/../input/%d-image.png) 2>/dev/null; ls {{cwd}}/../input"}`, id, id), `{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Seen."}}}`,
		`{"end": "end_turn"}`))
	topic := e.Say(0, "look at this", map[string]any{"files": []string{fmt.Sprint(id)}})
	e.MasterSaid(topic, 1)
	c := f.Call(4)
	at := filepath.Join(filepath.Dir(c.Cwd), "input", fmt.Sprintf("%d-image.png", id))
	named := regexp.MustCompile(`read-only: (\S+?\.png)`).FindStringSubmatch(string(c.Request("session/prompt")))
	if named == nil || filepath.Base(named[1]) != filepath.Base(at) {
		t.Fatalf("the turn is told the path of the picture: %v", named)
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(named[1])); err != nil || real != filepath.Dir(at) {
		t.Fatalf("the path told is the input folder beside the turn's work: %s %v", real, err)
	}
	if len(c.Shell) != 2 || strings.TrimSpace(c.Shell[0]["out"]) != fmt.Sprint(len(shot)) {
		t.Fatalf("the turn reads the picture at that path in its box: %+v", c.Shell)
	}
	if strings.TrimSpace(c.Shell[1]["out"]) != fmt.Sprintf("%d-image.png", id) {
		t.Fatalf("the turn neither puts a file into its input folder nor takes one away: %q", c.Shell[1]["out"])
	}
}

package egress

import (
	"deck2/tests/harness"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestAToolTakesItsWholeURLFromASecret: a private link the owner keeps as a secret is the URL of a tool, only https on
// the hosts of the tool, with the query of its input after it, and the link stays out of answers, errors and health.
func TestAToolTakesItsWholeURLFromASecret(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var seen []string
	svc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Query().Get("from") {
		case "json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"url": "https:\/\/%s%s"}`, r.Host, strings.ReplaceAll(r.URL.Path, "/", `\/`))
			return
		case "404":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "The requested URL %s was not found", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		fmt.Fprintf(w, "BEGIN:VCALENDAR\nURL:https://%s%s\nEND:VCALENDAR\n", r.Host, r.URL.Path)
	}))
	t.Cleanup(svc.Close)
	e := harness.New(t)
	link := svc.URL + "/owa/calendar/me@x.com/tok-0123456789/calendar.ics"
	e.Secret("cal", "outlook", link)
	tool := func(extra string) string {
		return harness.Manifest(fmt.Sprintf(`{"tools": {"http": [{"name": "ics", "description": "d", "method": "GET", "class": "internal",
      "url": "$secret.outlook?from={from}"%s, "input": {"type": "object", "properties": {"from": {"type": "string"}}, "required": ["from"]}}]}}`, extra))
	}
	if code, out := e.Call("deck.project_create", map[string]any{"name": "nohosts", "manifest": tool("")}); code != 422 || !strings.Contains(string(out), "needs hosts") {
		t.Fatalf("a url from a secret without hosts is refused: %d %s", code, out)
	}
	fixed := harness.Manifest(`{"tools": {"http": [{"name": "t", "description": "d", "method": "GET", "url": "https://a.com/x", "hosts": ["a.com"]}]}}`)
	if code, out := e.Call("deck.project_create", map[string]any{"name": "fixed", "manifest": fixed}); code != 422 || !strings.Contains(string(out), "hosts go only with a url from a secret") {
		t.Fatalf("hosts beside a written url are refused: %d %s", code, out)
	}
	e.Project("cal", tool(`, "hosts": ["127.0.0.1"]`))
	var got struct {
		Status int
		Text   string
	}
	e.Must("cal.ics", map[string]any{"from": "2026-10-26 00:00"}, &got)
	mu.Lock()
	first := seen[0]
	mu.Unlock()
	if first != "/owa/calendar/me@x.com/tok-0123456789/calendar.ics?from=2026-10-26+00%3A00" {
		t.Fatalf("the call goes to the link with the query of its input: %s", first)
	}
	if !strings.Contains(got.Text, "BEGIN:VCALENDAR") || strings.Contains(got.Text, "0123456789") || !strings.Contains(got.Text, "[secret]") {
		t.Fatalf("what the service says of the link reaches the caller as [secret]: %q", got.Text)
	}
	var echoed map[string]string
	e.Must("cal.ics", map[string]any{"from": "json"}, &echoed)
	if strings.Contains(echoed["url"], "0123456789") {
		t.Fatalf("a link echoed with escaped slashes is cut too: %v", echoed)
	}
	if code, out := e.Call("cal.ics", map[string]any{"from": "404"}); code == 200 || strings.Contains(string(out), "0123456789") {
		t.Fatalf("a page that echoes only the path keeps the token out of the error: %d %s", code, out)
	}
	for _, bad := range []string{link + "#x", strings.Replace(link, "https://", "http://", 1), "https://evil.example/tok-0123456789/calendar.ics"} {
		e.Secret("cal", "outlook", bad)
		code, out := e.Call("cal.ics", map[string]any{"from": "x"})
		if code == 200 || !strings.Contains(string(out), "no https address on the hosts of the tool, 127.0.0.1") || strings.Contains(string(out), "0123456789") {
			t.Fatalf("a link that is not https on the hosts of the tool is refused without showing it: %d %s", code, out)
		}
	}
	e.Secret("cal", "outlook", link)
	svc.Close()
	code, out := e.Call("cal.ics", map[string]any{"from": "x"})
	var hs []map[string]any
	e.Must("deck.health_list", nil, &hs)
	if code == 200 || strings.Contains(string(out), "0123456789") || strings.Contains(fmt.Sprint(hs), "0123456789") {
		t.Fatalf("a failed call keeps the link out of its error and of health: %d %s %v", code, out, hs)
	}
}

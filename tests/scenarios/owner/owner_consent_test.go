package owner

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"deck2/internal/health"
	"deck2/tests/harness"
)

// TestConsentWarningSpeaksTheOwnersLanguage: the warning that requests come too often or go unread is in the owner's
// language, each part alone and both together.
func TestConsentWarningSpeaksTheOwnersLanguage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		many, quick  bool
		english, has string
	}{
		{"many", true, false, "requests in a day", ""},
		{"unread", false, true, "yeses this week", ""},
		{"both", true, true, "requests in a day", "; "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "ext"}`) })
			e := harness.New(t, harness.WithClock())
			_, tok := e.Token("terminal", "notes.*")
			e.Project("notes", harness.PostManifest(svc.URL, ""))
			e.Secret("notes", "svc", "k")
			n := 4
			if c.many {
				n = 16
			}
			var asked []int64
			for i := range n {
				code, out := e.As(tok).Call("notes.post", map[string]any{"id": fmt.Sprint(i), "text": "hi"})
				if code != 200 {
					t.Fatalf("a call of the tool asks the owner: %d %s", code, out)
				}
			}
			for _, a := range e.Asks() {
				asked = append(asked, a.ID)
			}
			if c.quick {
				for _, id := range asked[:4] {
					e.Answer(id, "allow")
				}
			}
			e.Clock.Add(11 * time.Minute)
			e.Sweep()
			var rows []struct{ Subject, Status, Error string }
			e.In("ru").Must("deck.health_list", nil, &rows)
			for _, r := range rows {
				if r.Subject != health.Consent {
					continue
				}
				if strings.Contains(r.Error, c.english) || !regexp.MustCompile(`\p{Cyrillic}`).MatchString(r.Error) || !strings.Contains(r.Error, c.has) {
					t.Fatalf("the warning of consent speaks the owner's language: %q", r.Error)
				}
				return
			}
			t.Fatalf("no row of consent: %+v", rows)
		})
	}
}

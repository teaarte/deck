package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider/claudecode"
)

// TestRealMasterBuildsAModuleFromAPhrase: the master on the installed Claude Code builds a module from one chat
// phrase; the owner (this test) takes every recommendation, enables the draft and runs its command.
func TestRealMasterBuildsAModuleFromAPhrase(t *testing.T) {
	realCLI(t)
	phrase := os.Getenv("DECK_PHRASE")
	if phrase == "" {
		phrase = "Every morning, make me a list of the three top Hacker News stories, each with one line on why it matters."
	}
	home := t.TempDir()
	e := newEnvWith(t, home, claudecode.New(filepath.Join(home, "mcp")), realWaits)
	topic := e.say(0, phrase)
	deadline := time.Now().Add(25 * time.Minute)
	answered := map[int64]bool{}
	var draft string
	open := func() bool {
		var ts []chatTopic
		e.must("deck.chat_topics", nil, &ts)
		return len(ts) == 1 && ts[0].Reply != nil
	}
	for time.Now().Before(deadline) && (draft == "" || open()) {
		time.Sleep(3 * time.Second)
		var asks []askView
		e.must("deck.asks_list", nil, &asks)
		for _, a := range asks {
			if answered[a.ID] {
				continue
			}
			answered[a.ID] = true
			switch a.Kind {
			case "question":
				var p struct {
					Questions []struct {
						Question string
						Options  []struct{ Label string }
					}
				}
				_ = json.Unmarshal(a.Payload, &p)
				ans := map[string]string{}
				for _, q := range p.Questions {
					pick := "your recommendation"
					if len(q.Options) > 0 {
						pick = q.Options[0].Label
					}
					ans[q.Question] = pick
				}
				b, _ := json.Marshal(ans)
				t.Logf("question %d: %s -> %s", a.ID, a.Text, b)
				e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": string(b)}, nil)
			default:
				t.Logf("ask %d (%s): %s", a.ID, a.Kind, a.Text)
			}
		}
		var ps []struct{ Name, Status string }
		e.projectsOf(&ps)
		for _, p := range ps {
			if p.Status == "draft" {
				draft = p.Name
			}
		}
		ms := e.chatMessages(topic)
		last := ms[len(ms)-1]
		if last.Role == "master" && draft == "" && strings.Contains(last.Text, "?") && !answered[-last.ID] {
			answered[-last.ID] = true
			t.Logf("master: %s", last.Text)
			e.say(topic, "Go with your recommendation for everything and build it.")
		}
	}
	for _, m := range e.chatMessages(topic) {
		t.Logf("%s: %s", m.Role, m.Text)
	}
	if draft == "" {
		t.Fatal("the master made no draft module")
	}
	var m struct {
		Manifest string `json:"manifest"`
		Revision int64  `json:"revision"`
	}
	e.must("deck.manifest_get", map[string]any{"project": draft}, &m)
	t.Logf("draft %s at revision %d:\n%s", draft, m.Revision, m.Manifest)
	e.must("deck.project_enable", map[string]any{"project": draft, "revision": m.Revision}, nil)
	var man struct {
		Commands []struct {
			Name     string
			Executor struct{ Type string }
		}
	}
	_ = json.Unmarshal([]byte(m.Manifest), &man)
	if len(man.Commands) == 0 {
		t.Fatal("the module has no command")
	}
	var r struct{ Process int64 }
	e.must(draft+"."+man.Commands[0].Name, map[string]any{}, &r)
	got := e.pollState(r.Process, 15*time.Minute, 2*time.Second, "done", "error", "waiting_answer")
	t.Logf("run of %s.%s: %+v", draft, man.Commands[0].Name, got)
	if got.State != "done" {
		t.Fatalf("the enabled module runs: %+v", got)
	}
}

// TestRealMasterInterviewsACard has the master on the installed Claude Code interview the owner about a card by the
// interview skill; the owner (this test) takes every recommendation, and the spec ends up in the card.
func TestRealMasterInterviewsACard(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	e := newEnvWith(t, home, claudecode.New(filepath.Join(home, "mcp")), realWaits)
	e.must("deck.project_create", map[string]any{"name": "reports", "prompt": "# Reports: the weekly reports of the household budget.\n"}, nil)
	c := e.card(map[string]any{"project": "reports", "title": "Export the weekly report", "text": "I want to get the weekly report out of deck."})
	var out struct{ Topic int64 }
	e.must("deck.chat_send", map[string]any{"card": c.ID, "text": "Interview me about this card and write the spec into it. Ask at most three questions."}, &out)
	deadline := time.Now().Add(15 * time.Minute)
	answered := map[int64]bool{}
	asked := 0
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		var asks []askView
		e.must("deck.asks_list", nil, &asks)
		for _, a := range asks {
			if answered[a.ID] || a.Kind != "question" {
				continue
			}
			answered[a.ID] = true
			asked++
			var p struct {
				Questions []struct {
					Question string
					Options  []struct{ Label string }
				}
			}
			_ = json.Unmarshal(a.Payload, &p)
			ans := map[string]string{}
			for _, q := range p.Questions {
				ans[q.Question] = "your recommendation"
				if len(q.Options) > 0 {
					ans[q.Question] = q.Options[0].Label
				}
			}
			b, _ := json.Marshal(ans)
			t.Logf("question: %s -> %s", a.Text, b)
			e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": string(b)}, nil)
		}
		var got struct{ Text string }
		e.must("deck.card_get", map[string]any{"card": c.ID}, &got)
		var ts []chatTopic
		e.must("deck.chat_topics", nil, &ts)
		if strings.Contains(got.Text, "## Spec") && len(ts) == 1 && ts[0].Reply == nil {
			t.Logf("card after %d questions:\n%s", asked, got.Text)
			for _, m := range e.chatMessages(out.Topic) {
				t.Logf("%s: %s", m.Role, m.Text)
			}
			if !strings.HasPrefix(got.Text, "I want to get the weekly report out of deck.") {
				t.Fatalf("the owner's text stays first: %q", got.Text)
			}
			return
		}
	}
	t.Fatalf("no spec in the card: %+v", e.chatMessages(out.Topic))
}

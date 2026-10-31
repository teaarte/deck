package real

import (
	"deck2/internal/provider/claudecode"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
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
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home)), harness.WithConfig(realWaits), harness.WithWait(25*time.Minute))
	topic := e.Say(0, phrase)
	answered := map[int64]bool{}
	var draft string
	open := func() bool {
		var ts []harness.ChatTopic
		e.ChatTopics(false, &ts)
		return len(ts) == 1 && ts[0].Reply != nil
	}
	defer func() {
		for _, m := range e.ChatMessages(topic) {
			t.Logf("%s: %s", m.Role, m.Text)
		}
	}()
	e.Until("a draft module of the master", func() bool {
		var asks []harness.Ask
		e.Must("deck.asks_list", nil, &asks)
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
				e.Must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": string(b)}, nil)
			default:
				t.Logf("ask %d (%s): %s", a.ID, a.Kind, a.Text)
			}
		}
		var ps []struct{ Name, Status string }
		e.ProjectsOf(&ps)
		for _, p := range ps {
			if p.Status == "draft" {
				draft = p.Name
			}
		}
		ms := e.ChatMessages(topic)
		last := ms[len(ms)-1]
		if last.Role == "master" && draft == "" && strings.Contains(last.Text, "?") && !answered[-last.ID] {
			answered[-last.ID] = true
			t.Logf("master: %s", last.Text)
			e.Say(topic, "Go with your recommendation for everything and build it.")
		}
		return draft != "" && !open()
	})
	var m struct {
		Manifest string `json:"manifest"`
		Revision int64  `json:"revision"`
	}
	e.Must("deck.manifest_get", map[string]any{"project": draft}, &m)
	t.Logf("draft %s at revision %d:\n%s", draft, m.Revision, m.Manifest)
	e.Must("deck.project_enable", map[string]any{"project": draft, "revision": m.Revision}, nil)
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
	e.Must(draft+"."+man.Commands[0].Name, map[string]any{}, &r)
	got := e.WaitState(r.Process, "done", "error", "waiting_answer")
	t.Logf("run of %s.%s: %+v", draft, man.Commands[0].Name, got)
	if got.State != "done" {
		t.Fatalf("the enabled module runs: %+v", got)
	}
}

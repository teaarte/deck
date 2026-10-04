package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
)

// TestOwnerAttachesFilesAndTopicsKnowTheirModule: an attached file belongs to the message, the turn that takes
// it gets a copy in its input folder; a topic knows the module it was opened on.
func TestOwnerAttachesFilesAndTopicsKnowTheirModule(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "Read it."}
	})
	e.createProject("web", boardWebManifest)
	id := e.upload("master", "plan.txt", "text/plain", "the plan")
	topic := e.say(0, "look at the plan", map[string]any{"screen": `{"project":"web","screen":"main"}`, "files": []string{fmt.Sprint(id)}})
	e.masterSaid(topic, 1)
	ms := e.chatMessages(topic)
	var screen struct {
		Project string
		Files   []int64
	}
	_ = json.Unmarshal(ms[0].Screen, &screen)
	if !slices.Equal(screen.Files, []int64{id}) || strings.Contains(string(ms[0].Screen), "/") {
		t.Fatalf("the message keeps the id of the attached file, no path: %s", ms[0].Screen)
	}
	if raw, _ := json.Marshal(ms[0]); !strings.Contains(string(raw), fmt.Sprintf(`"files":[{"id":%d,"name":"plan.txt"}]`, id)) {
		t.Fatalf("the message lists its files by name: %s", raw)
	}
	prompt := e.Fake.Calls()[0].Prompt
	at := regexp.MustCompile(`(/\S+/input/` + fmt.Sprint(id) + `-plan\.txt)`).FindStringSubmatch(prompt)
	if at == nil {
		t.Fatalf("the master is told where the copy of the file is:\n%s", prompt)
	}
	if b, err := os.ReadFile(at[1]); err != nil || string(b) != "the plan" {
		t.Fatalf("the copy is in the input folder of the turn: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects")); err == nil {
		if m, _ := filepath.Glob(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", "*", "data", "attachments")); len(m) > 0 {
			t.Fatalf("nothing is copied into the data of master: %v", m)
		}
	}
	var ts []struct {
		ID      int64
		Project string
	}
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 1 || ts[0].Project != "web" {
		t.Fatalf("a topic knows the module of the screen it began on: %+v", ts)
	}
	e.refused("deck.chat_send", map[string]any{"text": "x", "files": []string{"nope"}}, 422, "the id of an upload")
	e.refused("deck.chat_send", map[string]any{"text": "again", "files": []string{fmt.Sprint(id)}}, 404, "no upload")
}

// TestARetriedMessageDoesNotAttachItsFilesAgain: a message sent again with its retry key is the one already
// sent, even when its upload is gone by now.
func TestARetriedMessageDoesNotAttachItsFilesAgain(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Read it."} })
	id := e.upload("master", "plan.txt", "text/plain", "the plan")
	in := map[string]any{"files": []string{fmt.Sprint(id)}, "retry_key": "k1"}
	topic := e.say(0, "look at the plan", in)
	if err := os.Remove(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Files(), fmt.Sprint(id))); err != nil {
		t.Fatal(err)
	}
	if again := e.say(0, "look at the plan", in); again != topic {
		t.Fatalf("the retry is the message already sent: %d, not %d", again, topic)
	}
}

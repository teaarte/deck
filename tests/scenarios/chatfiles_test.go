package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
)

// TestOwnerAttachesFilesAndTopicsKnowTheirModule: a file the owner attaches lands in the data folder of project
// master and its path goes with the message to the master; a topic knows the module it was opened on.
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
		Files   []string
	}
	_ = json.Unmarshal(ms[0].Screen, &screen)
	if len(screen.Files) != 1 || !strings.HasSuffix(screen.Files[0], "-plan.txt") || !strings.Contains(screen.Files[0], "/attachments/") {
		t.Fatalf("the message keeps the path of the attached file: %s", ms[0].Screen)
	}
	if b, err := os.ReadFile(screen.Files[0]); err != nil || string(b) != "the plan" {
		t.Fatalf("the file is in the data folder of project master: %q %v", b, err)
	}
	if req := e.Fake.Calls()[0]; !strings.Contains(req.Prompt, "the owner attached files: "+screen.Files[0]) {
		t.Fatalf("the master is told where the file is:\n%s", req.Prompt)
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

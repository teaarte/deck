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
	"time"

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

// TestTheFilesOfADeletedTopicGoWithTheCleanup: a deleted topic keeps its attachments no longer than the next
// daily cleanup.
func TestTheFilesOfADeletedTopicGoWithTheCleanup(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Read it."} })
	e.sweep()
	id := e.upload("master", "plan.txt", "text/plain", "the plan")
	topic := e.say(0, "look", map[string]any{"files": []string{fmt.Sprint(id)}})
	e.masterSaid(topic, 1)
	e.must("deck.chat_topic_delete", map[string]any{"topic": topic}, nil)
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Files(), fmt.Sprint(id))); !os.IsNotExist(err) {
		t.Fatalf("the attachment of a deleted topic is removed: %v", err)
	}
}

// TestADeletedTopicIsGoneAfterAMonth: the daily cleanup removes a topic deleted more than 30 days ago with its
// messages and summary; an archived topic stays.
func TestADeletedTopicIsGoneAfterAMonth(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} })
	e.sweep()
	gone := e.say(0, "drop me")
	e.masterSaid(gone, 1)
	kept := e.say(0, "keep me")
	e.masterSaid(kept, 1)
	e.must("deck.document_save", map[string]any{"project": "master", "name": fmt.Sprintf("topics/%d/summary.md", gone), "kind": "artifact", "text": "s"}, nil)
	e.must("deck.chat_topic_delete", map[string]any{"topic": gone}, nil)
	e.must("deck.chat_topic_archive", map[string]any{"topic": kept, "archived": true}, nil)
	e.Clock.Add(31 * 24 * time.Hour)
	e.sweep()
	var n int
	if err := e.App.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM chat_message WHERE topic_id = ?) + (SELECT COUNT(*) FROM chat_topic WHERE id = ?)
		+ (SELECT COUNT(*) FROM document WHERE name = ?)`, gone, gone, fmt.Sprintf("topics/%d/summary.md", gone)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the deleted topic, its messages and summary are gone: %d %v", n, err)
	}
	var ts []struct{ ID int64 }
	e.must("deck.chat_topics", map[string]any{"archived": true}, &ts)
	if len(ts) != 1 || ts[0].ID != kept {
		t.Fatalf("the archive stays: %+v", ts)
	}
}

// TestAnOldMessageWithFilePathsKeepsItsScreen: a message written when files were paths still tells the master
// the screen it was written on; its paths are skipped.
func TestAnOldMessageWithFilePathsKeepsItsScreen(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Read it."} })
	e.createProject("web", boardWebManifest)
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	if _, err := e.App.Store.DB.Exec(`INSERT INTO chat_message (topic_id, role, text, context) VALUES (?, 'owner', 'old plan', ?)`,
		topic, `{"project":"web","screen":"main","files":["/x/data/attachments/12-plan.txt"]}`); err != nil {
		t.Fatal(err)
	}
	e.say(topic, "and now")
	e.masterSaid(topic, 2)
	prompt := e.Fake.Calls()[1].Prompt
	if !strings.Contains(prompt, `written on project "web", screen "main"`) || !strings.Contains(prompt, "old plan") {
		t.Fatalf("the old message keeps its screen in the prompt:\n%s", prompt)
	}
}

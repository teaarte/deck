package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// pictureModule is a module whose model command draws a picture, and a collection of pictures a media block shows.
const pictureModule = `{"version": 11,
  "collections": [{"name": "pics", "description": "Pictures", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "file": {}}, "required": ["title"]}}],
  "commands": [
    {"name": "picture", "title": "Picture", "description": "Draws a scene.", "class": "internal",
     "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
     "executor": {"type": "model", "kind": "image", "prompt": "Draw the scene."}}],
  "screens": [{"name": "main", "title": "Pictures", "blocks": [
    {"type": "media", "title": "Pictures", "source": {"collection": "pics", "sort": "title"}, "item": {"file": "file", "title": "title"}}]}],
  "menu": ["main"]}`

// TestAFileOfAResultIsNamedByTheIdMediaTakes: a result names the file it made by the id it was kept under, and a
// media block takes that id as a number or as digits; the name the file had is no file.
func TestAFileOfAResultIsNamedByTheIdMediaTakes(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", pictureAnswer(0.04) },
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/gemini-3.1-flash-lite-image", "kind": "image"}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"openrouter/google/gemini-3.1-flash-lite-image"}}, nil)
	e.createProject("art", pictureModule)
	got := e.waitState(e.start("art", "picture", map[string]any{"scene": "a desk"}), "done", "error")
	var res struct {
		File  any `json:"file"`
		Files []struct {
			Path string `json:"path"`
			File int64  `json:"file"`
		} `json:"files"`
	}
	if got.State != "done" || json.Unmarshal(got.Result, &res) != nil || len(res.Files) != 1 || res.Files[0].File == 0 || res.File != fmt.Sprint(res.Files[0].File) {
		t.Fatalf("the result names its file by the id it was kept under: %s", got.Result)
	}
	id := res.Files[0].File
	var name string
	_ = e.App.Store.DB.QueryRow(`SELECT name FROM file WHERE id = ?`, id).Scan(&name)
	e.must("deck.records_upsert", map[string]any{"project": "art", "collection": "pics", "records": jsonl(
		map[string]any{"title": "a number", "file": id}, map[string]any{"title": "b digits", "file": fmt.Sprint(id)},
		map[string]any{"title": "c name", "file": name})}, nil)
	raw, _ := json.Marshal(e.screenMessages("art", "main", nil))
	if s := string(raw); strings.Count(s, fmt.Sprintf(`"url":"/api/files/%d"`, id)) != 2 || !strings.Contains(s, "a stored file is named by its id") {
		t.Fatalf("media shows the file by its id and its digits, not by its name: %s", s)
	}
}

// TestFilesOfAReplyShowInTheChat: what a reply of the master leaves in its results folder shows under its message; a
// link of its text is no file of it.
func TestFilesOfAReplyShowInTheChat(t *testing.T) {
	t.Parallel()
	var linked int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		results := c.Req.Dirs[len(c.Req.Dirs)-1]
		if err := os.WriteFile(filepath.Join(results, "voice-1.wav"), []byte("RIFF"), 0o600); err != nil {
			return provider.Result{IsError: true, Text: err.Error()}
		}
		return provider.Result{Text: fmt.Sprintf("Here it is, and [the old one](#/file/%d).", linked)}
	})
	e.createProject("art", pictureModule)
	linked = e.upload("art", "old.txt", "text/plain", "an older take")
	topic := e.say(0, "make a voice")
	e.masterSaid(topic, 1)
	var view struct {
		Messages []struct {
			Role  string
			Files []struct {
				ID   int64
				Name string
				URL  string
			}
		}
	}
	e.must("deck.chat", map[string]any{"topic": fmt.Sprint(topic)}, &view)
	m := view.Messages[len(view.Messages)-1]
	if m.Role != "master" || len(m.Files) != 1 || m.Files[0].Name != "voice-1.wav" {
		t.Fatalf("the reply shows the file it made, not the one it links to: %+v", m)
	}
}

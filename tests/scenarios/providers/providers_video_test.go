package providers

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const videoManifest = `{"version": 13,
  "commands": [
    {"name": "clip", "title": "Clip", "description": "Brings a scene to life.", "class": "internal",
     "input": {"type": "object", "properties": {"scene": {"type": "string"}, "first_frame": {"type": "string", "format": "file"},
       "last_frame": {"type": "string", "format": "file"}}},
     "executor": {"type": "model", "kind": "video", "prompt": "Slow camera push, no text.", "options": {"duration": 4, "aspect_ratio": "9:16"}}}
  ]}`

// quickVideo polls a job of a video often and gives it a ceiling of a second.
func quickVideo(c *app.Config) { c.VideoPoll, c.VideoCeiling = 20*time.Millisecond, time.Second }

// videoJob is a service of video jobs: a POST starts job-1, its polls answer the states in turn, the last one
// kept, and its content is a clip.
func videoJob(t *testing.T, states ...string) *harness.FakeModels {
	polls := 0
	return harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/videos": func(int, map[string]any) (int, string, string) {
			return 202, "application/json", `{"id": "job-1", "polling_url": "/api/v1/videos/job-1", "status": "pending"}`
		},
		"/api/v1/videos/job-1": func(int, map[string]any) (int, string, string) {
			polls++
			return 200, "application/json", states[min(polls, len(states))-1]
		},
		"/api/v1/videos/job-1/content": func(int, map[string]any) (int, string, string) {
			return 200, "video/mp4", "\x00\x00\x00\x18ftypmp42-clip"
		},
	})
}

// TestAVideoModelMakesAClipOfTheJobItStarted: a video job gets the prompt, options and frames, deck polls it and keeps
// the clip with its cost; a service that does not take the job leaves it to the next model.
func TestAVideoModelMakesAClipOfTheJobItStarted(t *testing.T) {
	t.Parallel()
	cloud := videoJob(t, `{"id": "job-1", "status": "in_progress"}`,
		`{"id": "job-1", "status": "completed", "unsigned_urls": ["x"], "usage": {"cost": 0.25, "is_byok": false}}`)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(down.Close)
	e := harness.New(t, harness.WithConfig(quickVideo))
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": down.URL + "/v1", "local": true}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": cloud.URL + "/api/v1", "key": "sk-or"}, nil)
	var m struct{ Kind string }
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "wan2.2", "name": "local-video", "kind": "video", "free": true}, &m)
	if m.Kind != "video" {
		t.Fatalf("a model makes video: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "alibaba/wan-2.6", "name": "wan", "kind": "video"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "video", "models": []string{"local-video", "wan"}}, nil)
	e.Project("shorts", videoManifest)
	frame := func(name, body string) string {
		var f struct{ ID int64 }
		e.Must("deck.file_upload", map[string]any{"project": "shorts", "name": name, "content": base64.StdEncoding.EncodeToString([]byte(body))}, &f)
		return fmt.Sprint(f.ID)
	}
	first, last := frame("first.png", "\x89PNG\r\n\x1a\nfirst"), frame("last.png", "\x89PNG\r\n\x1a\nlast")
	id := e.Start("shorts", "clip", map[string]any{"scene": "a knight meets a snail", "first_frame": first, "last_frame": last})
	var ceiling int64
	_ = e.App.Store.DB.QueryRow(`SELECT ceiling_ms FROM process WHERE id = ?`, id).Scan(&ceiling)
	if ceiling != time.Hour.Milliseconds() {
		t.Fatalf("a video command waits for its clip as long as an agent may work, not as a script: %d ms", ceiling)
	}
	got := e.WaitState(id, "done", "error")
	var res struct {
		Type  string   `json:"type"`
		Model string   `json:"model"`
		Cost  *float64 `json:"cost_usd"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if got.State != "done" || json.Unmarshal(got.Result, &res) != nil || res.Type != "video/mp4" || len(res.Files) != 1 || res.Model != "wan" ||
		res.Cost == nil || *res.Cost != 0.25 {
		t.Fatalf("the clip is a file of the result with the model that made it and its cost: %+v", got)
	}
	c := slices.DeleteFunc(cloud.Calls(), func(c harness.ModelCall) bool { return c.Path == "/api/v1/models" })
	if len(c) != 4 || c[0].Path != "/api/v1/videos" || c[1].Path != "/api/v1/videos/job-1" || c[3].Path != "/api/v1/videos/job-1/content" || c[3].Auth != "Bearer sk-or" {
		t.Fatalf("deck starts the job, polls it until it is done and takes its content with its key: %+v", c)
	}
	body := c[0].Body
	frames, _ := json.Marshal(body["frame_images"])
	want := `[{"frame_type":"first_frame","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfirst")) +
		`"},"type":"image_url"},{"frame_type":"last_frame","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nlast")) +
		`"},"type":"image_url"}]`
	if c[0].Model != "alibaba/wan-2.6" || string(frames) != want || body["duration"] != 4.0 || body["aspect_ratio"] != "9:16" ||
		!strings.Contains(fmt.Sprint(body["prompt"]), "Slow camera push") || !strings.Contains(fmt.Sprint(body["prompt"]), "a knight meets a snail") {
		t.Fatalf("the job gets the model, the prompt with the input, the frames as pictures and the options: %s %v", frames, body)
	}
	var spend []harness.SpendRow
	e.ModelCalls("shorts", 0, &spend)
	if len(spend) != 1 || spend[0].Provider != "wan" || spend[0].Cost == nil || *spend[0].Cost != 0.25 {
		t.Fatalf("the clip is counted once, with the cost its job reported: %+v", spend)
	}
	var status, text string
	_ = e.App.Store.DB.QueryRow(`SELECT status, error FROM health WHERE subject = 'model:local-video' AND workspace_id IS NOT NULL`).Scan(&status, &text)
	if status != "error" || text != "answered HTTP 503; wan takes its work" {
		t.Fatalf("the model that did not take the job is red and names the one that took it: %s %q", status, text)
	}
}

// TestAVideoJobThatFailsOrNeverEndsIsAnError: a job the service failed is an error with its words and id, a job
// past its ceiling is an error with its id; neither goes to another model once a service took it.
func TestAVideoJobThatFailsOrNeverEndsIsAnError(t *testing.T) {
	t.Parallel()
	failed := videoJob(t, `{"id": "job-1", "status": "failed", "error": {"code": "moderation", "message": "the prompt was refused"}, "usage": {"is_byok": false}}`)
	stuck := videoJob(t, `{"id": "job-1", "status": "in_progress"}`)
	spare := videoJob(t, `{"id": "job-1", "status": "completed"}`)
	e := harness.New(t, harness.WithConfig(quickVideo))
	for name, api := range map[string]*harness.FakeModels{"failed": failed, "stuck": stuck, "spare": spare} {
		e.Must("deck.connection_set", map[string]any{"name": name, "api": "openai", "url": api.URL + "/api/v1"}, nil)
		e.Must("deck.model_set", map[string]any{"connection": name, "model": "veo", "name": name, "kind": "video", "call_usd": 0.4}, nil)
	}
	e.Project("shorts", videoManifest)
	for _, c := range []struct{ first, want string }{{"failed", "the prompt was refused"}, {"stuck", "not ready in 1s"}} {
		e.Must("deck.assignment_set", map[string]any{"role": "video", "models": []string{c.first, "spare"}}, nil)
		got := e.WaitState(e.Start("shorts", "clip", map[string]any{"scene": "a knight"}), "done", "error")
		if got.State != "error" || !strings.Contains(got.Error, c.want) || !strings.Contains(got.Error, "job-1") {
			t.Fatalf("%s: the job is an error with what happened and its id: %+v", c.first, got)
		}
	}
	if n := len(spare.Calls()); n != 0 {
		t.Fatalf("a job a service took goes to no other model: %d calls", n)
	}
	var spend []harness.SpendRow
	e.ModelCalls("shorts", 0, &spend)
	costs := map[string]float64{}
	for _, s := range spend {
		if s.Cost != nil {
			costs[s.Provider] += *s.Cost
		}
	}
	if len(spend) != 2 || costs["failed"] != 0 || costs["stuck"] != 0.4 {
		t.Fatalf("a failed job costs nothing it did not report, one past its ceiling is priced by its model: %+v", spend)
	}
}

// TestAVideoCommandOfAManifestTakesAPromptAndItsFrames: a video command needs a prompt, and its file fields are its
// first and last frames, a file each.
func TestAVideoCommandOfAManifestTakesAPromptAndItsFrames(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	noPrompt := strings.Replace(videoManifest, `"prompt": "Slow camera push, no text.", `, "", 1)
	e.Refused("deck.project_create", map[string]any{"name": "shorts", "manifest": noPrompt}, 422, "a video model needs prompt or prompt_file")
	otherFile := strings.Replace(videoManifest, `"last_frame": {"type": "string", "format": "file"}`, `"sound": {"type": "string", "format": "file"}`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "shorts", "manifest": otherFile}, 422, "first_frame and last_frame")
	frames := strings.Replace(videoManifest, `"last_frame": {"type": "string", "format": "file"}`,
		`"last_frame": {"type": "array", "items": {"type": "string", "format": "file"}}`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "shorts", "manifest": frames}, 422, "input field last_frame is a list of files")
	e.Project("shorts", videoManifest)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = ?
		WHERE document_id IN (SELECT id FROM document WHERE kind = 'manifest' AND project_id = (SELECT id FROM project WHERE name = 'shorts'))`, frames); err != nil {
		t.Fatal(err)
	}
	var got struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "shorts"}, &got)
	if !strings.Contains(strings.Join(got.Warnings, "\n"), "last_frame is a list of files") {
		t.Fatalf("a saved module with a list of files still loads, with a warning: %v", got.Warnings)
	}
}

// TestALongCallOfAnAgentTellsItThatItStillRuns: an agent that asks for progress hears that its long call still runs,
// so a client that cuts idle calls keeps waiting for the clip.
func TestALongCallOfAnAgentTellsItThatItStillRuns(t *testing.T) {
	t.Parallel()
	api := videoJob(t, `{"id": "job-1", "status": "in_progress"}`, `{"id": "job-1", "status": "in_progress"}`,
		`{"id": "job-1", "status": "in_progress"}`, `{"id": "job-1", "status": "completed", "usage": {"cost": 0.1}}`)
	var out string
	var bad bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad = c.Slow(ctx, "model_call", map[string]any{"kind": "video", "prompt": "a knight rides"})
		return provider.Result{Text: "done"}
	}), harness.WithConfig(

		quickVideo, func(c *app.Config) { c.Progress = 10 * time.Millisecond }))

	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "alibaba/wan-2.6", "name": "wan", "kind": "video", "call_usd": 0.1}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "video", "models": []string{"wan"}}, nil)
	e.Project("studio", `{"version": 13, "models": ["@video"], "commands": [{"name": "animate", "title": "Animate",
	  "description": "Brings a scene to life.", "class": "internal", "executor": {"type": "agent", "prompt": "Make the clip."}}]}`)
	if got := e.WaitState(e.Start("studio", "animate", map[string]any{}), "done", "error"); got.State != "done" || bad || !strings.Contains(out, `"type":"video/mp4"`) {
		t.Fatalf("the agent gets the clip: %+v %s", got, out)
	}
	if n := e.Fake.Progress.Load(); n == 0 {
		t.Fatalf("a call that waits for a job tells the agent it still runs")
	}
}

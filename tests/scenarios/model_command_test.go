package scenarios

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// modelManifest is a project with commands of type model: a fact check by a schema, a picture, speech and the
// words of a recording.
const modelManifest = `{"version": 9,
  "commands": [
    {"name": "factcheck", "title": "Fact check", "description": "Checks a claim.", "class": "internal",
     "input": {"type": "object", "properties": {"claim": {"type": "string"}}, "required": ["claim"]},
     "result": {"type": "object", "properties": {"verdict": {"enum": ["true", "false", "unclear"]}, "confidence": {"type": "number"}},
       "required": ["verdict", "confidence"], "additionalProperties": false},
     "executor": {"type": "model", "kind": "text", "prompt": "Check the claim against what is known."}},
    {"name": "picture", "title": "Picture", "description": "Draws a scene.", "class": "internal",
     "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
     "executor": {"type": "model", "kind": "image", "prompt": "Draw the scene, no text in the picture.", "options": {"image_config": {"aspect_ratio": "9:16"}}}},
    {"name": "voice", "title": "Voice", "description": "Speaks a line.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]},
     "executor": {"type": "model", "kind": "tts", "prompt": "Calm, like a documentary.", "options": {"voice": "onyx", "model": "an-ungranted-model"}}},
    {"name": "words", "title": "Words", "description": "Writes down a recording.", "class": "internal",
     "input": {"type": "object", "properties": {"voice": {"type": "string", "format": "file"}}, "required": ["voice"]},
     "executor": {"type": "model", "kind": "stt", "options": {"timestamp_granularities[]": "word"}}},
    {"name": "track", "title": "Track", "description": "Makes background music.", "class": "internal",
     "input": {"type": "object", "properties": {"mood": {"type": "string"}}},
     "executor": {"type": "model", "kind": "music", "prompt": "Instrumental background music, no vocals.", "options": {"seconds": 40}}}
  ]}`

func chatText(content string) string {
	b, _ := json.Marshal(map[string]any{"id": "c-1", "model": "fact-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 200, "completion_tokens": 20}})
	return string(b)
}

// TestAModelCommandAnswersByItsSchema: a command of type model sends its prompt and input to the model assigned to
// text with its result schema, and the JSON the model answers is its result.
func TestAModelCommandAnswersByItsSchema(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText("```json\n{\"verdict\": \"false\", \"confidence\": 0.9}\n```")
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text", "input_usd": 0.5, "output_usd": 1}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.createProject("shorts", modelManifest)
	got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "Snails beat knights in 1300"}), "done", "error")
	if got.State != "done" || got.NotBySchema || string(got.Result) != `{"confidence":0.9,"verdict":"false"}` {
		t.Fatalf("the model's JSON is the result: %+v", got)
	}
	c := api.calls()
	if len(c) != 1 || c[0].Auth != "Bearer gsk-1" || c[0].Model != "fact-model" {
		t.Fatalf("one call to the assigned model with deck's key: %+v", c)
	}
	msg := fmt.Sprint(c[0].Body["messages"])
	schema := c[0].Body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
	if !strings.Contains(msg, "Check the claim") || !strings.Contains(msg, "Snails beat knights") || schema["required"] == nil {
		t.Fatalf("the model gets the prompt, the input and the result schema: %v %v", msg, schema)
	}
	var spend []spendRow
	e.modelCalls("shorts", 0, &spend)
	if len(spend) != 1 || spend[0].Command != "factcheck" || spend[0].Process != got.ID || spend[0].Cost == nil || *spend[0].Cost != 0.00012 {
		t.Fatalf("the call is the command's in the journal, priced by its model: %+v", spend)
	}
}

// TestModelCommandsMakeFilesOfPicturesAndSpeechAndTextOfRecordings: a picture (from chat completions when the API has
// no images path) and speech are files of the result; stt sends the recording of the input field.
func TestModelCommandsMakeFilesOfPicturesAndSpeechAndTextOfRecordings(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", pictureAnswer(0.04) },
		"/api/v1/audio/speech":     func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
		"/api/v1/audio/transcriptions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"text": "Knights against snails", "words": [{"word": "Knights", "start": 0.1, "end": 0.5}]}`
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	for kind, model := range map[string]string{"image": "google/gemini-3.1-flash-lite-image", "tts": "openai/gpt-5-mini-tts", "stt": "openai/whisper-1"} {
		e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": model, "kind": kind}, nil)
		e.must("deck.assignment_set", map[string]any{"role": kind, "models": []string{"openrouter/" + model}}, nil)
	}
	e.createProject("shorts", modelManifest)
	for _, c := range []struct {
		command string
		input   map[string]any
		file    string
	}{{"picture", map[string]any{"scene": "a painter at a desk"}, "image/png"}, {"voice", map[string]any{"text": "Knights against snails"}, "audio/mpeg"}} {
		got := e.waitState(e.start("shorts", c.command, c.input), "done", "error")
		var res struct {
			Type  string `json:"type"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		var kept int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE type = ?`, c.file).Scan(&kept)
		if got.State != "done" || json.Unmarshal(got.Result, &res) != nil || res.Type != c.file || len(res.Files) != 1 || kept != 1 {
			t.Fatalf("%s: what the model made is a file of the result: %+v, %d kept", c.command, got, kept)
		}
	}
	calls := api.made()
	if len(calls) != 3 || calls[0].Path != "/api/v1/images/generations" || calls[1].Path != "/api/v1/chat/completions" ||
		calls[1].Body["image_config"].(map[string]any)["aspect_ratio"] != "9:16" || fmt.Sprint(calls[1].Body["modalities"]) != "[image text]" {
		t.Fatalf("a picture is asked of the images path, then of chat completions with the options of the command: %+v", calls)
	}
	if calls[2].Body["input"] != "Knights against snails" || calls[2].Model != "openai/gpt-5-mini-tts" || calls[2].Body["voice"] != "onyx" || calls[2].Body["instructions"] != "Calm, like a documentary." {
		t.Fatalf("speech speaks the text field with the voice of the options (never their model) and the prompt as instructions: %+v", calls[2].Body)
	}
	var f struct{ ID int64 }
	e.must("deck.file_upload", map[string]any{"project": "shorts", "name": "voice.mp3", "content": base64.StdEncoding.EncodeToString([]byte("ID3-recorded"))}, &f)
	got := e.waitState(e.start("shorts", "words", map[string]any{"voice": fmt.Sprint(f.ID)}), "done", "error")
	if got.State != "done" || !strings.Contains(string(got.Result), `"text":"Knights against snails"`) || !strings.Contains(string(got.Result), `"words"`) {
		t.Fatalf("the words of the recording are the result: %+v", got)
	}
	if last := api.made()[3]; last.Path != "/api/v1/audio/transcriptions" {
		t.Fatalf("the recording goes to transcriptions: %+v", last)
	}
}

// TestAModelCommandOfATrialWaitsForTheOwner: a trial calls a paid model only on the owner's yes; meanwhile its
// process parks, and the yes makes the call and ends the process with the answer.
func TestAModelCommandOfATrialWaitsForTheOwner(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText(`{"verdict": "true", "confidence": 0.6}`)
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text"}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.createProject("shorts", modelManifest)
	var tried struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"live": true, "only": true, "command": "shorts.factcheck", "input": map[string]any{"claim": "two"}}, &tried)
	e.waitState(tried.Process, "waiting_answer")
	if n := len(api.made()); n != 0 {
		t.Fatalf("a trial's call of a paid model does not reach it before the yes: %d calls", n)
	}
	a := e.waitAsk()
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	if got := e.waitState(tried.Process, "done", "error"); got.State != "done" || string(got.Result) != `{"confidence":0.6,"verdict":"true"}` {
		t.Fatalf("the owner's yes makes the call and the process ends with its answer: %+v", got)
	}
}

// TestAModelCallSpeaksTheOpenAICompatibleProtocol: a connection of the Google API says how to reach it, and a base
// from before moves a Google connection to the OpenAI-compatible address of Google.
func TestAModelCallSpeaksTheOpenAICompatibleProtocol(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1beta/openai/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText(`{"verdict": "unclear", "confidence": 0.4}`)
		},
	})
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.must("deck.connection_set", map[string]any{"name": "native", "api": "google", "url": api.URL + "/v1beta", "key": "AIza-1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "native", "model": "gemini-3.8-flash", "name": "native-gemini", "kind": "text"}, nil)
	e.createProject("shorts", strings.Replace(modelManifest, `"kind": "text",`, `"kind": "text", "model": "native-gemini",`, 1))
	got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	if got.State != "error" || !strings.Contains(got.Error, "OpenAI-compatible") || len(api.made()) != 0 {
		t.Fatalf("a connection of the Google API says to use its OpenAI-compatible address: %+v", got)
	}
	e.olderBase(12)
	if _, err := e.App.Store.DB.Exec(`INSERT INTO connection (workspace_id, name, api, url, key_secret) VALUES (1, 'google', 'google', 'https://generativelanguage.googleapis.com/v1beta', 'google')`); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	var api2, url string
	if err := e.App.Store.DB.QueryRow(`SELECT api, url FROM connection WHERE name = 'google'`).Scan(&api2, &url); err != nil {
		t.Fatal(err)
	}
	if api2 != "openai" || url != "https://generativelanguage.googleapis.com/v1beta/openai/" {
		t.Fatalf("a Google connection of a base from before is on the OpenAI-compatible address of Google: %s %s", api2, url)
	}
	e.must("deck.connection_set", map[string]any{"name": "native", "api": "openai", "url": api.URL + "/v1beta/openai", "key": "AIza-1"}, nil)
	got = e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	if got.State != "done" || string(got.Result) != `{"confidence":0.4,"verdict":"unclear"}` || api.made()[0].Auth != "Bearer AIza-1" {
		t.Fatalf("the OpenAI-compatible address of Google answers: %+v %+v", got, api.made())
	}
}

// made are the calls the fake got past reading its catalog.
func (f *fakeModels) made() []modelCall {
	var out []modelCall
	for _, c := range f.calls() {
		if !strings.HasSuffix(c.Path, "/models") {
			out = append(out, c)
		}
	}
	return out
}

// TestAModelCommandGetsWhatItsKindNeeds: a manifest whose model command lacks what its kind needs is refused with
// what to give it.
func TestAModelCommandGetsWhatItsKindNeeds(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range []struct{ from, to, want string }{
		{`"kind": "text", "prompt": "Check the claim against what is known."`, `"kind": "text"`, "needs prompt or prompt_file"},
		{`"kind": "text",`, `"kind": "text", "model": "@image",`, "is @text or a model by name"},
		{`"properties": {"text": {"type": "string"}}, "required": ["text"]`, `"properties": {"line": {"type": "string"}}`, "speaks the input field text"},
		{`"properties": {"voice": {"type": "string", "format": "file"}}`, `"properties": {"voice": {"type": "string"}}`, "one input field of format file"},
		{`"kind": "stt"`, `"kind": "video"`, "manifest does not match the format"},
	} {
		code, out := e.call("deck.project_create", map[string]any{"name": "shorts", "manifest": strings.Replace(modelManifest, c.from, c.to, 1)})
		if code == 200 || !strings.Contains(string(out), c.want) {
			t.Fatalf("%s: refused with %q, got %d %s", c.to, c.want, code, out)
		}
	}
}

// TestAModelThatBrokeOffAfterTheRequestHasNoFallback: an answer cut after the request reached the model may have
// been billed, so the call fails rather than ask the fallback.
func TestAModelThatBrokeOffAfterTheRequestHasNoFallback(t *testing.T) {
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(cut.Close)
	spare := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText(`{"verdict": "true", "confidence": 1}`)
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "cut", "api": "openai", "url": cut.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "spare", "api": "openai", "url": spare.URL + "/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "cut", "model": "a", "kind": "text", "free": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "spare", "model": "b", "kind": "text", "free": true}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"cut/a", "spare/b"}}, nil)
	e.createProject("shorts", modelManifest)
	if got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "x"}), "done", "error"); got.State != "error" || len(spare.made()) != 0 {
		t.Fatalf("a cut answer is an error and the fallback is not asked: %+v", got)
	}
}

// TestAModelInUseAndAConnectionWithModelsStay: a model an agent still runs on and a connection with models are not
// removed; a removed model leaves its assignment to the next one, and the connection goes once its models have.
func TestAModelInUseAndAConnectionWithModelsStay(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText(`{"verdict": "true", "confidence": 0.5}`)
		},
	})
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, `{"hang": true}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", writtenManifest), "work")
	e.waitFor("the turn", func() bool { return f.calls() >= 4 && f.call(4).request("session/prompt") != nil })
	e.refused("deck.model_remove", map[string]any{"name": "acp"}, 422, "still run on acp")
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	e.waitFor("the turn to end", func() bool {
		code, _ := e.call("deck.model_remove", map[string]any{"name": "acp"})
		return code == 200
	})

	e.must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	for _, m := range []string{"fact-model", "spare-model"} {
		e.must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text", "input_usd": 0.5, "output_usd": 1}, nil)
	}
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model", "groq/spare-model"}}, nil)
	e.refused("deck.connection_remove", map[string]any{"name": "groq"}, 422, "remove the models first")
	e.must("deck.model_remove", map[string]any{"name": "groq/fact-model"}, nil)
	e.createProject("shorts", modelManifest)
	got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "Owls are birds"}), "done", "error")
	if c := api.calls(); got.State != "done" || len(c) != 1 || c[0].Model != "spare-model" {
		t.Fatalf("the assignment goes on with the next model: %+v %+v", got, c)
	}
	e.refused("deck.connection_remove", map[string]any{"name": "groq"}, 422, "model groq/spare-model")
	e.must("deck.model_remove", map[string]any{"name": "groq/spare-model"}, nil)
	e.must("deck.connection_remove", map[string]any{"name": "groq"}, nil)
	var ms struct{ Connections []struct{ Name string } }
	e.must("deck.models_list", nil, &ms)
	for _, c := range ms.Connections {
		if c.Name == "groq" {
			t.Fatalf("the connection without models goes: %+v", ms.Connections)
		}
	}
}

// TestAModelThatDoesNotAnswerIsRedWhileItsFallbackWorks: a model of the owner's machine that does not answer turns
// its health red and the assignment shows the fallback that took the work; its first answer turns it green.
func TestAModelThatDoesNotAnswerIsRedWhileItsFallbackWorks(t *testing.T) {
	cloud := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", pictureAnswer(0.04) },
	})
	local := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"data": [{"b64_json": "iVBORw0KGgo="}]}`
		},
	})
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": down.URL + "/v1", "local": true}, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": cloud.URL + "/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "media", "model": "z-image", "name": "local-image", "kind": "image", "free": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.04}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"local-image", "openrouter/painter"}}, nil)
	e.createProject("shorts", modelManifest)
	health := func() (string, string) {
		var status, text string
		_ = e.App.Store.DB.QueryRow(`SELECT status, error FROM health WHERE subject = 'provider:local-image'`).Scan(&status, &text)
		return status, text
	}
	using := func() string {
		var m modelsOut
		e.must("deck.models_list", nil, &m)
		for _, a := range m.Assignments {
			if a.Role == "image" {
				return a.Using
			}
		}
		return ""
	}
	if got := e.waitState(e.start("shorts", "picture", map[string]any{"scene": "a knight"}), "done", "error"); got.State != "done" {
		t.Fatalf("the fallback draws the picture: %+v", got)
	}
	if status, text := health(); status != "error" || text != "did not answer; openrouter/painter takes its work" || using() != "openrouter/painter" {
		t.Fatalf("the model that did not answer is red, names the one that took its work, and the assignment shows it: %s %q %s", status, text, using())
	}
	var rows []struct{ Subject, Status string }
	e.must("deck.health_list", nil, &rows)
	if !slices.ContainsFunc(rows, func(r struct{ Subject, Status string }) bool {
		return r.Subject == "provider:local-image" && r.Status == "error"
	}) {
		t.Fatalf("the red model is on the list of health: %+v", rows)
	}
	refusing := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) {
			return 401, "application/json", `{"error": "bad key"}`
		},
	})
	e.must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": refusing.URL + "/v1", "local": true}, nil)
	e.waitState(e.start("shorts", "picture", map[string]any{"scene": "a knight"}), "done", "error")
	if status, _ := health(); status != "error" {
		t.Fatalf("a refusal is no good answer: %s", status)
	}
	e.must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": local.URL + "/v1", "local": true}, nil)
	if got := e.waitState(e.start("shorts", "picture", map[string]any{"scene": "a knight"}), "done", "error"); got.State != "done" || len(local.calls()) != 1 {
		t.Fatalf("the first model draws again once it is up: %+v", got)
	}
	if status, _ := health(); status != "ok" || using() != "local-image" {
		t.Fatalf("its first answer turns it green: %s %s", status, using())
	}
}

// audioStream is how a chat model streams the audio it makes in the OpenAI protocol: base64 pieces in delta.audio,
// the usage with its cost at the end.
func audioStream(pieces ...string) string {
	var b strings.Builder
	for _, p := range pieces {
		c, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"audio": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(p))}}}}})
		fmt.Fprintf(&b, "data: %s\n\n", c)
	}
	b.WriteString(`data: {"choices": [], "usage": {"prompt_tokens": 12, "completion_tokens": 0, "cost": 0.08}}` + "\n\ndata: [DONE]\n\n")
	return b.String()
}

// TestAMusicModelMakesATrackOfItsOwnPathOrOfChatAudio: music is asked of the audio/music path the way speech is, and
// of chat completions with audio out when the API has no such path; either way the track is a file of the result.
func TestAMusicModelMakesATrackOfItsOwnPathOrOfChatAudio(t *testing.T) {
	local := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/music": func(int, map[string]any) (int, string, string) {
			return 200, "audio/wav", "RIFF\x24\x00\x00\x00WAVEfmt "
		},
	})
	cloud := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "text/event-stream", audioStream("ID3\x04\x00", "-track")
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": local.URL + "/v1", "local": true}, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": cloud.URL + "/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "media", "model": "medium", "name": "local-music", "kind": "music", "free": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria", "kind": "music", "call_usd": 0.08}, nil)
	e.createProject("shorts", modelManifest)
	track := func(model, want string) {
		t.Helper()
		e.must("deck.assignment_set", map[string]any{"role": "music", "models": []string{model}}, nil)
		got := e.waitState(e.start("shorts", "track", map[string]any{"mood": "calm"}), "done", "error")
		var res struct {
			Type  string   `json:"type"`
			Model string   `json:"model"`
			Cost  *float64 `json:"cost_usd"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if got.State != "done" || json.Unmarshal(got.Result, &res) != nil || res.Type != want || len(res.Files) != 1 || res.Model != model {
			t.Fatalf("%s: the track is a file of the result with the model that made it: %+v", model, got)
		}
		if model == "lyria" && (res.Cost == nil || *res.Cost != 0.08) {
			t.Fatalf("the result says what the track cost: %+v", got)
		}
	}
	track("local-music", "audio/wav")
	if c := local.calls(); len(c) != 1 || c[0].Model != "medium" || c[0].Body["seconds"] != 40.0 ||
		!strings.Contains(fmt.Sprint(c[0].Body["input"]), "no vocals") || !strings.Contains(fmt.Sprint(c[0].Body["input"]), "calm") {
		t.Fatalf("music is asked of audio/music with the prompt and input as input and the options: %+v", c)
	}
	track("lyria", "audio/mpeg")
	c := cloud.calls()
	if len(c) != 2 || c[0].Path != "/api/v1/audio/music" || c[1].Path != "/api/v1/chat/completions" ||
		fmt.Sprint(c[1].Body["modalities"]) != "[audio text]" || c[1].Body["stream"] != true {
		t.Fatalf("without an audio/music path music is asked of chat completions with audio out, streamed: %+v", c)
	}
	var spend []spendRow
	e.modelCalls("shorts", 0, &spend)
	var cost float64
	for _, s := range spend {
		if s.Provider == "lyria" && s.Cost != nil {
			cost += *s.Cost
		}
	}
	if cost != 0.08 {
		t.Fatalf("the cloud track is counted with its cost: %+v", spend)
	}
}

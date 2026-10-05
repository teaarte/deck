package scenarios

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// modelManifest is a project with commands of type model: a fact check by a schema, a picture, speech and the
// words of a recording.
const modelManifest = `{"version": 7,
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
     "executor": {"type": "model", "kind": "stt", "options": {"timestamp_granularities[]": "word"}}}
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
	e.must("deck.llm_calls_list", map[string]any{"project": "shorts"}, &spend)
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

// TestAModelCommandPastTheLimitWaitsForTheOwner: past the day of its connection the call of a model command waits
// for the owner and its process parks; the owner's yes makes it and the process ends with the answer.
func TestAModelCommandPastTheLimitWaitsForTheOwner(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", chatText(`{"verdict": "true", "confidence": 0.6}`)
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "daily_usd": 0.0001}, nil)
	e.must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text", "call_usd": 0.001}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.createProject("shorts", modelManifest)
	if got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "one"}), "done", "error"); got.State != "done" {
		t.Fatalf("the first call is within the day: %+v", got)
	}
	id := e.start("shorts", "factcheck", map[string]any{"claim": "two"})
	e.waitState(id, "waiting_answer")
	if n := len(api.calls()); n != 1 {
		t.Fatalf("past the limit nothing reaches the model: %d calls", n)
	}
	a := e.waitAsk()
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	if got := e.waitState(id, "done", "error"); got.State != "done" || string(got.Result) != `{"confidence":0.6,"verdict":"true"}` {
		t.Fatalf("the owner's yes makes the call and the process ends with its answer: %+v", got)
	}
}

// TestAModelCommandOnGoogleSpeaksItsOwnAPI: text on a google connection goes to generateContent with the key in its
// own header and the schema as the response schema.
func TestAModelCommandOnGoogleSpeaksItsOwnAPI(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1beta/models/gemini-3.8-flash:generateContent": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"candidates": [{"content": {"parts": [{"text": "{\"verdict\": \"unclear\", \"confidence\": 0.4}"}]}}],
			  "usageMetadata": {"promptTokenCount": 120, "candidatesTokenCount": 12}, "modelVersion": "gemini-3.8-flash"}`
		},
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "google", "api": "google", "url": api.URL + "/v1beta", "key": "AIza-1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "google", "model": "gemini-3.8-flash", "name": "gemini", "kind": "text"}, nil)
	e.createProject("shorts", strings.Replace(modelManifest, `"kind": "text",`, `"kind": "text", "model": "gemini",`, 1))
	got := e.waitState(e.start("shorts", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	if got.State != "done" || string(got.Result) != `{"confidence":0.4,"verdict":"unclear"}` {
		t.Fatalf("the answer of Google is the result: %+v", got)
	}
	c := api.made()
	cfg, _ := c[0].Body["generationConfig"].(map[string]any)
	if len(c) != 1 || c[0].Auth != "" || cfg["responseMimeType"] != "application/json" || cfg["responseJsonSchema"] == nil {
		t.Fatalf("Google gets its own request with the schema: %+v", c)
	}
	var spend []spendRow
	e.must("deck.llm_calls_list", map[string]any{"project": "shorts"}, &spend)
	if len(spend) != 1 || spend[0].Provider != "google" {
		t.Fatalf("the call is in the journal: %+v", spend)
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

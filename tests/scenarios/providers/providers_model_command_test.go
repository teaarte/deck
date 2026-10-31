package providers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestAModelCommandAnswersByItsSchema: a command of type model sends its prompt and input to the model assigned to
// text with its result schema, and the JSON the model answers is its result.
func TestAModelCommandAnswersByItsSchema(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText("```json\n{\"verdict\": \"false\", \"confidence\": 0.9}\n```")
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text", "input_usd": 0.5, "output_usd": 1}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "Snails beat knights in 1300"}), "done", "error")
	if got.State != "done" || got.NotBySchema || string(got.Result) != `{"confidence":0.9,"verdict":"false"}` {
		t.Fatalf("the model's JSON is the result: %+v", got)
	}
	c := api.Calls()
	if len(c) != 1 || c[0].Auth != "Bearer gsk-1" || c[0].Model != "fact-model" {
		t.Fatalf("one call to the assigned model with deck's key: %+v", c)
	}
	msg := fmt.Sprint(c[0].Body["messages"])
	schema := c[0].Body["response_format"].(map[string]any)["json_schema"].(map[string]any)["schema"].(map[string]any)
	if !strings.Contains(msg, "Check the claim") || !strings.Contains(msg, "Snails beat knights") || schema["required"] == nil {
		t.Fatalf("the model gets the prompt, the input and the result schema: %v %v", msg, schema)
	}
	var spend []harness.SpendRow
	e.ModelCalls("films", 0, &spend)
	if len(spend) != 1 || spend[0].Command != "factcheck" || spend[0].Process != got.ID || spend[0].Cost == nil || *spend[0].Cost != 0.00012 {
		t.Fatalf("the call is the command's in the journal, priced by its model: %+v", spend)
	}
}

// TestModelCommandsMakeFilesOfPicturesAndSpeechAndTextOfRecordings: a picture (from chat completions when the API has
// no images path) and speech are files of the result; stt sends the recording of the input field.
func TestModelCommandsMakeFilesOfPicturesAndSpeechAndTextOfRecordings(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.PictureAnswer(0.04)
		},
		"/api/v1/audio/speech": func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
		"/api/v1/audio/transcriptions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"text": "Knights against snails", "words": [{"word": "Knights", "start": 0.1, "end": 0.5}]}`
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	for kind, model := range map[string]string{"image": "google/gemini-3.1-flash-lite-image", "tts": "openai/gpt-5-mini-tts", "stt": "openai/whisper-1"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": model, "kind": kind}, nil)
		e.Must("deck.assignment_set", map[string]any{"role": kind, "models": []string{"openrouter/" + model}}, nil)
	}
	e.Project("films", harness.ModelManifest)
	for _, c := range []struct {
		command string
		input   map[string]any
		file    string
	}{{"picture", map[string]any{"scene": "a painter at a desk"}, "image/png"}, {"voice", map[string]any{"text": "Knights against snails"}, "audio/mpeg"}} {
		got := e.WaitState(e.Start("films", c.command, c.input), "done", "error")
		var res struct {
			Type  string `json:"type"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		var fs []struct{ Type string }
		e.Must("deck.files_list", map[string]any{"project": "films"}, &fs)
		kept := 0
		for _, f := range fs {
			if f.Type == c.file {
				kept++
			}
		}
		if got.State != "done" || json.Unmarshal(got.Result, &res) != nil || res.Type != c.file || len(res.Files) != 1 || kept != 1 {
			t.Fatalf("%s: what the model made is a file of the result: %+v, %d kept", c.command, got, kept)
		}
	}
	calls := made(api)
	if len(calls) != 3 || calls[0].Path != "/api/v1/images/generations" || calls[1].Path != "/api/v1/chat/completions" ||
		calls[1].Body["image_config"].(map[string]any)["aspect_ratio"] != "9:16" || fmt.Sprint(calls[1].Body["modalities"]) != "[image text]" {
		t.Fatalf("a picture is asked of the images path, then of chat completions with the options of the command: %+v", calls)
	}
	if calls[2].Body["input"] != "Knights against snails" || calls[2].Model != "openai/gpt-5-mini-tts" || calls[2].Body["voice"] != "onyx" || calls[2].Body["instructions"] != "Calm, like a documentary." {
		t.Fatalf("speech speaks the text field with the voice of the options (never their model) and the prompt as instructions: %+v", calls[2].Body)
	}
	var f struct{ ID int64 }
	e.Must("deck.file_upload", map[string]any{"project": "films", "name": "voice.mp3", "content": base64.StdEncoding.EncodeToString([]byte("ID3-recorded"))}, &f)
	got := e.WaitState(e.Start("films", "words", map[string]any{"voice": fmt.Sprint(f.ID)}), "done", "error")
	if got.State != "done" || !strings.Contains(string(got.Result), `"text":"Knights against snails"`) || !strings.Contains(string(got.Result), `"words"`) {
		t.Fatalf("the words of the recording are the result: %+v", got)
	}
	if last := made(api)[3]; last.Path != "/api/v1/audio/transcriptions" {
		t.Fatalf("the recording goes to transcriptions: %+v", last)
	}
}

// TestAModelCommandOfATrialWaitsForTheOwner: a trial calls a paid model only on the owner's yes; meanwhile its
// process parks, and the yes makes the call and ends the process with the answer.
func TestAModelCommandOfATrialWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.6}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "only": true, "command": "films.factcheck", "input": map[string]any{"claim": "two"}}, &tried)
	e.WaitState(tried.Process, "waiting_answer")
	if n := len(made(api)); n != 0 {
		t.Fatalf("a trial's call of a paid model does not reach it before the yes: %d calls", n)
	}
	a := e.WaitAsk()
	e.Must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	if got := e.WaitState(tried.Process, "done", "error"); got.State != "done" || string(got.Result) != `{"confidence":0.6,"verdict":"true"}` {
		t.Fatalf("the owner's yes makes the call and the process ends with its answer: %+v", got)
	}
}

// TestAModelCallSpeaksTheOpenAICompatibleProtocol: a connection of the Google API says how to reach it, and the
// OpenAI-compatible address of Google answers.
func TestAModelCallSpeaksTheOpenAICompatibleProtocol(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1beta/openai/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "unclear", "confidence": 0.4}`)
		},
	})
	e := harness.New(t, harness.WithProvider(&harness.Fake{}))
	e.Must("deck.connection_set", map[string]any{"name": "native", "api": "google", "url": api.URL + "/v1beta", "key": "AIza-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "native", "model": "gemini-3.8-flash", "name": "native-gemini", "kind": "text"}, nil)
	e.Project("films", strings.Replace(harness.ModelManifest, `"kind": "text",`, `"kind": "text", "model": "native-gemini",`, 1))
	got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	if got.State != "error" || !strings.Contains(got.Error, "OpenAI-compatible") || len(made(api)) != 0 {
		t.Fatalf("a connection of the Google API says to use its OpenAI-compatible address: %+v", got)
	}
	e.Must("deck.connection_set", map[string]any{"name": "native", "api": "openai", "url": api.URL + "/v1beta/openai", "key": "AIza-1"}, nil)
	got = e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	if got.State != "done" || string(got.Result) != `{"confidence":0.4,"verdict":"unclear"}` || made(api)[0].Auth != "Bearer AIza-1" {
		t.Fatalf("the OpenAI-compatible address of Google answers: %+v %+v", got, made(api))
	}
}

// made are the calls the fake got past reading its catalog.
func made(f *harness.FakeModels) []harness.ModelCall {
	var out []harness.ModelCall
	for _, c := range f.Calls() {
		if !strings.HasSuffix(c.Path, "/models") {
			out = append(out, c)
		}
	}
	return out
}

// TestAModelCommandGetsWhatItsKindNeeds: a manifest whose model command lacks what its kind needs is refused with
// what to give it.
func TestAModelCommandGetsWhatItsKindNeeds(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, c := range []struct{ from, to, want string }{
		{`"kind": "text", "prompt": "Check the claim against what is known."`, `"kind": "text"`, "needs prompt or prompt_file"},
		{`"kind": "text",`, `"kind": "text", "model": "@image",`, "is @text or a model by name"},
		{`"properties": {"text": {"type": "string"}}, "required": ["text"]`, `"properties": {"line": {"type": "string"}}`, "speaks the input field text"},
		{`"properties": {"voice": {"type": "string", "format": "file"}}`, `"properties": {"voice": {"type": "string"}}`, "one input field of format file"},
		{`"kind": "stt"`, `"kind": "hologram"`, "manifest does not match the format"},
	} {
		code, out := e.Call("deck.project_create", map[string]any{"name": "films", "manifest": strings.Replace(harness.ModelManifest, c.from, c.to, 1)})
		if code == 200 || !strings.Contains(string(out), c.want) {
			t.Fatalf("%s: refused with %q, got %d %s", c.to, c.want, code, out)
		}
	}
}

// TestAModelThatBrokeOffAfterTheRequestHasNoFallback: an answer cut after the request reached the model may have
// been billed, so the call fails rather than ask the fallback.
func TestAModelThatBrokeOffAfterTheRequestHasNoFallback(t *testing.T) {
	t.Parallel()
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(cut.Close)
	spare := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 1}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "cut", "api": "openai", "url": cut.URL + "/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "spare", "api": "openai", "url": spare.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cut", "model": "a", "kind": "text", "free": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "spare", "model": "b", "kind": "text", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"cut/a", "spare/b"}}, nil)
	e.Project("films", harness.ModelManifest)
	if got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "x"}), "done", "error"); got.State != "error" || len(made(spare)) != 0 {
		t.Fatalf("a cut answer is an error and the fallback is not asked: %+v", got)
	}
}

// TestAModelInUseAndAConnectionWithModelsStay: a model an agent still runs on and a connection with models are not
// removed; a removed model leaves its assignment to the next one, and the connection goes once its models have.
func TestAModelInUseAndAConnectionWithModelsStay(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.5}`)
		},
	})
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, `{"hang": true}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", writtenManifest), "work")
	e.Until("the turn", func() bool { return f.Calls() >= 4 && f.Call(4).Request("session/prompt") != nil })
	e.Refused("deck.model_remove", map[string]any{"name": "acp"}, 422, "still run on acp")
	e.Must("deck.process_stop", map[string]any{"process": id}, nil)
	e.WaitState(id, "stopped")
	e.Until("the turn to end", func() bool {
		code, _ := e.Call("deck.model_remove", map[string]any{"name": "acp"})
		return code == 200
	})

	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	for _, m := range []string{"fact-model", "spare-model"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text", "input_usd": 0.5, "output_usd": 1}, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model", "groq/spare-model"}}, nil)
	e.Refused("deck.connection_remove", map[string]any{"name": "groq"}, 422, "remove the models first")
	e.Must("deck.model_remove", map[string]any{"name": "groq/fact-model"}, nil)
	e.Project("films", harness.ModelManifest)
	got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "Owls are birds"}), "done", "error")
	if c := api.Calls(); got.State != "done" || len(c) != 1 || c[0].Model != "spare-model" {
		t.Fatalf("the assignment goes on with the next model: %+v %+v", got, c)
	}
	e.Refused("deck.connection_remove", map[string]any{"name": "groq"}, 422, "model groq/spare-model")
	e.Must("deck.model_remove", map[string]any{"name": "groq/spare-model"}, nil)
	e.Must("deck.connection_remove", map[string]any{"name": "groq"}, nil)
	var ms struct{ Connections []struct{ Name string } }
	e.Must("deck.models_list", nil, &ms)
	for _, c := range ms.Connections {
		if c.Name == "groq" {
			t.Fatalf("the connection without models goes: %+v", ms.Connections)
		}
	}
}

// TestAModelThatDoesNotAnswerIsRedWhileItsFallbackWorks: a model of the owner's machine that does not answer turns
// its health red and the assignment shows the fallback that took the work; its first answer turns it green.
func TestAModelThatDoesNotAnswerIsRedWhileItsFallbackWorks(t *testing.T) {
	t.Parallel()
	cloud := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.PictureAnswer(0.04)
		},
	})
	local := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"data": [{"b64_json": "iVBORw0KGgo="}]}`
		},
	})
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": down.URL + "/v1", "local": true}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": cloud.URL + "/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "z-image", "name": "local-image", "kind": "image", "free": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.04}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"local-image", "openrouter/painter"}}, nil)
	e.Project("films", harness.ModelManifest)
	health := func() (string, string) {
		var status, text string
		if h := e.HealthRowOf("model:local-image"); h != nil {
			status, _ = h["status"].(string)
			text, _ = h["error"].(string)
		}
		return status, text
	}
	using := func() string {
		var m harness.ModelsOut
		e.Must("deck.models_list", nil, &m)
		for _, a := range m.Assignments {
			if a.Role == "image" {
				return a.Using
			}
		}
		return ""
	}
	if got := e.WaitState(e.Start("films", "picture", map[string]any{"scene": "a knight"}), "done", "error"); got.State != "done" {
		t.Fatalf("the fallback draws the picture: %+v", got)
	}
	if status, text := health(); status != "error" || text != "did not answer; openrouter/painter takes its work" || using() != "openrouter/painter" {
		t.Fatalf("the model that did not answer is red, names the one that took its work, and the assignment shows it: %s %q %s", status, text, using())
	}
	rows := e.Health()
	if !slices.ContainsFunc(rows, func(r harness.HealthRow) bool {
		return r.Subject == "model:local-image" && r.Status == "error"
	}) {
		t.Fatalf("the red model is on the list of health: %+v", rows)
	}
	refusing := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) {
			return 401, "application/json", `{"error": "bad key"}`
		},
	})
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": refusing.URL + "/v1", "local": true}, nil)
	e.WaitState(e.Start("films", "picture", map[string]any{"scene": "a knight"}), "done", "error")
	if status, _ := health(); status != "error" {
		t.Fatalf("a refusal is no good answer: %s", status)
	}
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": local.URL + "/v1", "local": true}, nil)
	if got := e.WaitState(e.Start("films", "picture", map[string]any{"scene": "a knight"}), "done", "error"); got.State != "done" || len(local.Calls()) != 1 {
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
	t.Parallel()
	local := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/music": func(int, map[string]any) (int, string, string) {
			return 200, "audio/wav", "RIFF\x24\x00\x00\x00WAVEfmt "
		},
	})
	cloud := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "text/event-stream", audioStream("ID3\x04\x00", "-track")
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": local.URL + "/v1", "local": true}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": cloud.URL + "/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "medium", "name": "local-music", "kind": "music", "free": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria", "kind": "music", "call_usd": 0.08}, nil)
	e.Project("films", harness.ModelManifest)
	track := func(model, want string) {
		t.Helper()
		e.Must("deck.assignment_set", map[string]any{"role": "music", "models": []string{model}}, nil)
		got := e.WaitState(e.Start("films", "track", map[string]any{"mood": "calm"}), "done", "error")
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
	if c := local.Calls(); len(c) != 1 || c[0].Model != "medium" || c[0].Body["seconds"] != 40.0 ||
		!strings.Contains(fmt.Sprint(c[0].Body["input"]), "no vocals") || !strings.Contains(fmt.Sprint(c[0].Body["input"]), "calm") {
		t.Fatalf("music is asked of audio/music with the prompt and input as input and the options: %+v", c)
	}
	track("lyria", "audio/mpeg")
	c := cloud.Calls()
	if len(c) != 2 || c[0].Path != "/api/v1/audio/music" || c[1].Path != "/api/v1/chat/completions" ||
		fmt.Sprint(c[1].Body["modalities"]) != "[audio text]" || c[1].Body["stream"] != true {
		t.Fatalf("without an audio/music path music is asked of chat completions with audio out, streamed: %+v", c)
	}
	var spend []harness.SpendRow
	e.ModelCalls("films", 0, &spend)
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

// TestAModelPricedPerCharOrSecondCostsWhatItTook: a model priced per character or second of its unit costs the
// characters deck sent or the seconds the API reported; a module reads the models and cost of its process.
func TestAModelPricedPerCharOrSecondCostsWhatItTook(t *testing.T) {
	t.Parallel()
	heard := 0
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/audio/speech": func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
		"/api/v1/audio/transcriptions": func(int, map[string]any) (int, string, string) {
			heard++
			return 200, "application/json", []string{`{"text": "hi", "usage": {"type": "duration", "seconds": 4.8}}`,
				`{"text": "hi", "duration": 3}`, `{"text": "hi"}`}[min(heard-1, 2)]
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	e.Refused("deck.model_set", map[string]any{"connection": "openrouter", "model": "fish", "kind": "tts", "unit": "char", "input_usd": 15, "output_usd": 1}, 422, "char")
	var m struct{ Unit string }
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "fish", "name": "fish", "kind": "tts", "unit": "char", "input_usd": 15}, &m)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "whisper", "name": "whisper", "kind": "stt", "unit": "second", "input_usd": 100}, nil)
	if m.Unit != "char" {
		t.Fatalf("the model says its unit: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "fish", "name": "fish", "kind": "tts"}, &m)
	if m.Unit != "char" {
		t.Fatalf("a model changed without a unit keeps its own: %+v", m)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"fish"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "stt", "models": []string{"whisper"}}, nil)
	e.Project("films", harness.ModelManifest)
	var voice struct {
		Cost *float64 `json:"cost_usd"`
	}
	if got := e.WaitState(e.Start("films", "voice", map[string]any{"text": "Hello wörld"}), "done", "error"); json.Unmarshal(got.Result, &voice) != nil ||
		voice.Cost == nil || math.Abs(*voice.Cost-11*15/1e6) > 1e-12 {
		t.Fatalf("speech costs the characters deck sent: %+v", got)
	}
	var f struct{ ID int64 }
	e.Must("deck.file_upload", map[string]any{"project": "films", "name": "voice.mp3", "content": base64.StdEncoding.EncodeToString([]byte("ID3-recorded"))}, &f)
	var costs []*float64
	for range 3 {
		id := e.Start("films", "words", map[string]any{"voice": fmt.Sprint(f.ID)})
		if got := e.WaitState(id, "done", "error"); got.State != "done" || strings.Contains(string(got.Result), "cost") {
			t.Fatalf("the words are the result as the API gave them: %+v", got)
		}
		var p struct {
			Models []string `json:"models"`
			Cost   *float64 `json:"cost_usd"`
		}
		e.Must("deck.process_get", map[string]any{"process": id}, &p)
		if !slices.Equal(p.Models, []string{"whisper"}) {
			t.Fatalf("the process names the model it called: %+v", p)
		}
		costs = append(costs, p.Cost)
	}
	if costs[0] == nil || math.Abs(*costs[0]-4.8*100/1e6) > 1e-12 || costs[1] == nil || math.Abs(*costs[1]-3*100/1e6) > 1e-12 || costs[2] != nil {
		t.Fatalf("a recording costs the seconds the API reported, and nothing it did not: %v %v %v", costs[0], costs[1], costs[2])
	}
}

// TestTwoModelsOfOneAPIModelKeepTheirOwnNames: two models of deck on the same model of an API, with their own
// options and price, are each called and counted by their own name.
func TestTwoModelsOfOneAPIModelKeepTheirOwnNames(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText("hi")
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "name": "first", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "name": "second", "kind": "text", "call_usd": 0.02, "options": `{"temperature": 0}`}, nil)
	e.Must("deck.model_call", map[string]any{"kind": "text", "model": "second", "prompt": "hi"}, nil)
	var calls []struct {
		Name string  `json:"gen_ai.provider.name"`
		Cost float64 `json:"cost_usd"`
	}
	e.ModelCalls("", 0, &calls)
	if len(calls) == 0 || calls[0].Name != "second" || calls[0].Cost != 0.02 {
		t.Fatalf("the call is the model it named: %+v", calls)
	}
}

// TestAModelCommandNoModelTakesIsWarnedOfAndFailsAtOnce: a model command whose role has no model is warned of and
// yellow in health; its trial fails at once with where to assign one, waiting for no answer.
func TestAModelCommandNoModelTakesIsWarnedOfAndFailsAtOnce(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(harness.ModelManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var imported struct{ Warnings []string }
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": "films"}, &imported)
	if len(imported.Warnings) != 4 || !strings.Contains(imported.Warnings[0], "command factcheck: it calls @text, but no model is assigned to text") ||
		!strings.Contains(imported.Warnings[0], "Settings → Models") {
		t.Fatalf("the import names each model command no model takes: %v", imported.Warnings)
	}
	if h := e.WaitHealth("command:films.factcheck", "warning"); !strings.Contains(h.Error, "pick one in Settings → Models") {
		t.Fatalf("the draft's health shows it in yellow: %+v", h)
	}
	var m struct {
		Warnings []string
		Manifest string
	}
	e.Must("deck.manifest_get", map[string]any{"project": "films"}, &m)
	if len(m.Warnings) != 4 {
		t.Fatalf("the manifest is read with the warnings: %v", m.Warnings)
	}
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"command": "films.factcheck", "input": map[string]any{"claim": "x"}}, &tried)
	got := e.WaitState(tried.Process, "done", "error", "waiting_answer")
	if got.State != "error" || !strings.Contains(got.Error, "no model is assigned to text") || !strings.Contains(got.Error, "Settings → Models") {
		t.Fatalf("the trial fails at once with where to assign a model: %+v", got)
	}
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "gsk-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	var saved struct{ Warnings []string }
	e.Must("deck.manifest_save", map[string]any{"project": "films", "manifest": m.Manifest}, &saved)
	if len(saved.Warnings) != 3 || slices.ContainsFunc(saved.Warnings, func(w string) bool { return strings.Contains(w, "factcheck") }) {
		t.Fatalf("a save warns of the commands still without a model: %v", saved.Warnings)
	}
}

// TestALocalServerOfPicturesTakesOneCallAtATime: calls of a local model of pictures wait in deck's queue rather
// than at its server, and the first good answer of a model never called turns its health green.
func TestALocalServerOfPicturesTakesOneCallAtATime(t *testing.T) {
	t.Parallel()
	var now, most atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := now.Add(1)
		defer now.Add(-1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"b64_json": "iVBORw0KGgo="}]}`))
	}))
	t.Cleanup(srv.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": srv.URL + "/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "klein", "name": "local-image", "kind": "image", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"local-image"}}, nil)
	e.Project("films", harness.ModelManifest)
	ids := []int64{}
	for range 3 {
		ids = append(ids, e.Start("films", "picture", map[string]any{"scene": "a cube"}))
	}
	for _, id := range ids {
		if got := e.WaitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("every call is answered: %+v", got)
		}
	}
	if most.Load() != 1 {
		t.Fatalf("the server computes one call at a time, not %d", most.Load())
	}
	var status string
	if h := e.HealthRowOf("model:local-image"); h != nil {
		status, _ = h["status"].(string)
	}
	if status != "ok" {
		t.Fatalf("a model that answered is ok, not never called: %q", status)
	}
}

// TestAnImageModelEditsThePictureInFile: the file of an image command goes as a form to images/edits with the prompt
// and options; an API without that path gets it in chat completions as an image_url beside the prompt.
func TestAnImageModelEditsThePictureInFile(t *testing.T) {
	t.Parallel()
	png := "\x89PNG\r\n\x1a\nsample"
	type got struct{ path, ct, image, prompt, steps, model, format, chat string }
	var mu sync.Mutex
	var seen []got
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := got{path: r.URL.Path}
		if strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/edit/v1/images/edits" && r.ParseMultipartForm(1<<20) == nil {
			f, h, err := r.FormFile("image")
			if err == nil {
				b, _ := io.ReadAll(f)
				g.image, g.ct = string(b), h.Header.Get("Content-Type")
			}
			g.prompt, g.steps, g.model, g.format = r.FormValue("prompt"), r.FormValue("steps"), r.FormValue("model"), r.FormValue("response_format")
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			b, _ := io.ReadAll(r.Body)
			g.chat = string(b)
		}
		mu.Lock()
		seen = append(seen, g)
		mu.Unlock()
		switch r.URL.Path {
		case "/edit/v1/images/edits":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data": [{"b64_json": "iVBORw0KGgo="}]}`)
		case "/chat/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, harness.PictureAnswer(0.04))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": srv.URL + "/edit/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "chat", "api": "openai", "url": srv.URL + "/chat/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "flux-edit", "kind": "image"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "chat", "model": "gemini-image", "kind": "image"}, nil)
	e.Project("films", harness.Manifest(`{"commands": [
	  {"name": "redraw", "title": "Redraw", "description": "Edits a picture.", "class": "internal",
	   "input": {"type": "object", "properties": {"picture": {"type": "string", "format": "file"}}, "required": ["picture"]},
	   "executor": {"type": "model", "kind": "image", "prompt": "Make the cube blue.", "options": {"steps": 4}}}]}`))
	var f struct{ ID int64 }
	e.Must("deck.file_upload", map[string]any{"project": "films", "name": "cube.png", "content": base64.StdEncoding.EncodeToString([]byte(png))}, &f)
	for _, model := range []string{"media/flux-edit", "chat/gemini-image"} {
		e.Must("deck.assignment_set", map[string]any{"role": "image", "models": []string{model}}, nil)
		p := e.WaitState(e.Start("films", "redraw", map[string]any{"picture": fmt.Sprint(f.ID)}), "done", "error")
		if p.State != "done" || !strings.Contains(string(p.Result), `"type":"image/png"`) {
			t.Fatalf("%s: the edited picture is the file of the result: %+v", model, p)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("an edit goes to images/edits, the other connection falls back to chat completions: %+v", seen)
	}
	if e0 := seen[0]; e0.image != png || e0.ct != "image/png" || e0.prompt != "Make the cube blue." || e0.steps != "4" || e0.model != "flux-edit" || e0.format != "b64_json" {
		t.Fatalf("the picture, the prompt, the model and the options are fields of the form: %+v", e0)
	}
	if seen[1].path != "/chat/v1/images/edits" || !strings.Contains(seen[2].chat, `"image_url":{"url":"data:image/png;base64,`+base64.StdEncoding.EncodeToString([]byte(png))) ||
		!strings.Contains(seen[2].chat, `"text":"Make the cube blue."`) {
		t.Fatalf("chat completions get the picture as an image_url beside the prompt: %+v", seen[1:])
	}
}

// TestSpeechTakesItsVoiceFromTheOptionsOfItsModel: deck sends no voice of its own, a refusal without one says where to
// set it; with one it speaks mp3, its check is one phrase, and a tts price from the catalog counts characters.
func TestSpeechTakesItsVoiceFromTheOptionsOfItsModel(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/audio/speech": func(_ int, body map[string]any) (int, string, string) {
			if body["voice"] == nil {
				return 400, "application/json", `{"error": {"message": "Unknown voice"}}`
			}
			return 200, "audio/mpeg", "ID3-voice"
		},
		"/api/v1/models": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"data": [{"id": "elevenlabs/eleven-v4", "pricing": {"prompt": "0.0001"}, "architecture": {"output_modalities": ["audio"]}}]}`
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or"}, nil)
	var m struct {
		Kind, Unit string
		InputUSD   float64 `json:"input_usd"`
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "elevenlabs/eleven-v4", "name": "eleven"}, &m)
	if m.Kind != "tts" || m.Unit != "char" || math.Abs(m.InputUSD-100) > 1e-9 {
		t.Fatalf("a tts price of the catalog counts characters: %+v", m)
	}
	check := map[string]any{"kind": "tts", "model": "eleven", "text": "This is a short check of the voice."}
	e.Refused("deck.model_call", check, 422, "set voice in the options of the model")
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "elevenlabs/eleven-v4", "name": "eleven", "options": `{"voice": "21m00Tcm4TlvDq8ikWAM"}`}, nil)
	var models struct {
		Models []struct{ Name, Options string }
	}
	e.Must("deck.models_list", map[string]any{}, &models)
	if !slices.ContainsFunc(models.Models, func(x struct{ Name, Options string }) bool {
		return x.Name == "eleven" && x.Options == `{"voice": "21m00Tcm4TlvDq8ikWAM"}`
	}) {
		t.Fatalf("the models list gives the options of a model to its form: %+v", models)
	}
	var out struct{ Type string }
	e.Must("deck.model_call", check, &out)
	got := api.Calls()
	last := got[len(got)-1]
	if out.Type != "audio/mpeg" || last.Body["voice"] != "21m00Tcm4TlvDq8ikWAM" || last.Body["response_format"] != "mp3" || last.Body["input"] != check["text"] {
		t.Fatalf("speech goes with the voice of the options as mp3: %+v %+v", out, last)
	}
}

// TestABigOptionOfAModelIsAFileOfIt: a long value of the options of a model is kept as its file, the models list gives
// its name and size, a call sends it whole, a save of what the form shows keeps it, and new options drop it.
func TestABigOptionOfAModelIsAFileOfIt(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": api.URL + "/v1", "local": true}, nil)
	sample := strings.Repeat("UklGRg", 20000)
	options, _ := json.Marshal(map[string]any{"voice": "own", "ref_audio": sample})
	set := map[string]any{"connection": "local", "model": "qwen3-tts", "name": "voice", "kind": "tts", "options": string(options)}
	e.Must("deck.model_set", set, nil)
	shown := func() string {
		var models struct {
			Models []struct{ Name, Options string }
		}
		e.Must("deck.models_list", map[string]any{}, &models)
		for _, m := range models.Models {
			if m.Name == "voice" {
				return m.Options
			}
		}
		return ""
	}
	form := shown()
	if len(form) > 200 || !strings.Contains(form, `"$file"`) || !strings.Contains(form, `"size":120000`) || !strings.Contains(form, `"voice":"own"`) {
		t.Fatalf("the form gets the name and size of a big option, not the value: %s", form)
	}
	check := map[string]any{"kind": "tts", "model": "voice", "text": "Hello."}
	e.Must("deck.model_call", check, nil)
	if got := api.Calls(); got[len(got)-1].Body["ref_audio"] != sample {
		t.Fatal("a call sends the big option whole")
	}
	other := e.Upload("", "other.txt", "text/plain", "not of the model")
	set["options"] = fmt.Sprintf(`{"ref_audio": {"$file": %d}}`, other)
	e.Refused("deck.model_set", set, 422, "no file of model voice")
	if shown() != form {
		t.Fatalf("options naming a file of another are not saved: %s", shown())
	}
	set["options"] = form
	e.Must("deck.model_set", set, nil)
	e.Must("deck.model_call", check, nil)
	if got := api.Calls(); got[len(got)-1].Body["ref_audio"] != sample || shown() != form {
		t.Fatalf("what the form showed saves back to the same file: %s", shown())
	}
	set["options"] = `{"voice": "own"}`
	e.Must("deck.model_set", set, nil)
	var files int
	// The files of the options of a model, which files_list leaves out.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE owner_kind = 'model'`).Scan(&files)
	if files != 0 {
		t.Fatalf("a file the options no longer name goes: %d", files)
	}
}

// TestTheLastModelAtItsLimitIsTheModelsFailure: a model command whose last model answers 429 fails as the model's
// limit, red in the health of that model, and does not go to the master; a refused key is the model's login.
func TestTheLastModelAtItsLimitIsTheModelsFailure(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "keyless" {
				return 403, "application/json", `{"error": {"message": "the key is refused"}}`
			}
			return 429, "application/json", `{"error": {"message": "rate limited"}}`
		},
	})
	e := harness.New(t, harness.WithAgent(harness.MasterScript), harness.WithClock())
	e.MasterAwake()
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "keyless", "kind": "text"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "one"}), "done", "error").ID)
	if got.ErrorType != "model.limit" || got.Whose != "model" {
		t.Fatalf("the last model at its limit is the model's failure: %+v", got)
	}
	if h := e.HealthRowOf("model:groq/fact-model"); h == nil || h["status"] != "error" {
		t.Fatalf("the model at its limit is red in health: %v", h)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/keyless"}}, nil)
	if got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "two"}), "done", "error").ID); got.ErrorType != "model.login" {
		t.Fatalf("a refused key is the model's login: %+v", got)
	}
	e.App.Runner.Wait()
	if n := len(e.RunsOf("master", "on_error")); n != 0 {
		t.Fatalf("a failure of a model does not go to the master: %d", n)
	}
	for tok, challenged := range map[string]bool{"": false, "deck_nope": true} {
		req, _ := http.NewRequest("POST", e.URL+"/api/commands/deck.model_call", strings.NewReader(`{"model": "groq/keyless", "kind": "text", "prompt": "hi"}`))
		if e.Sign(req); tok != "" {
			harness.Bearer(tok)(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 || (resp.Header.Get("WWW-Authenticate") != "") != challenged {
			t.Fatalf("only deck's refusal of its token challenges the client, not a model's refused key: %s %d %v", tok, resp.StatusCode, resp.Header)
		}
	}
}

// TestATextModelOfAnAPIHasACheckThatCallsIt: a text model outside an agent has a check in Settings → Models, one
// short call that shows in the status of its row.
func TestATextModelOfAnAPIHasACheckThatCallsIt(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1beta/openai/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText("ok")
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "google", "api": "openai", "url": api.URL + "/v1beta/openai", "key": "AIza-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "google", "model": "gemini-3.8-flash", "name": "gem", "kind": "text"}, nil)
	check := e.RowAction("", "settings", "deck.model_call", map[string]any{"kind": "text", "model": "gem"})
	if check == nil || check["verb"] != "check" {
		t.Fatalf("the row of a text model has a check: %v", check)
	}
	if code, out := e.Press(check); code != 200 || len(made(api)) != 1 {
		t.Fatalf("the check is one call of the model: %d %s %+v", code, out, made(api))
	}
	var models struct {
		Models []struct{ Name, Status string }
	}
	e.Must("deck.models_list", map[string]any{}, &models)
	if !slices.ContainsFunc(models.Models, func(m struct{ Name, Status string }) bool { return m.Name == "gem" && m.Status == "ok" }) {
		t.Fatalf("the model that answered is ok in its row: %+v", models)
	}
}

// TestARequestPastTheLimitPerMinuteIsTheModelsLimit: a 413 whose words name a limit per minute is the model's limit,
// taken over by the next model without a pause; a 413 of a request too big for the model at all is the input's.
func TestARequestPastTheLimitPerMinuteIsTheModelsLimit(t *testing.T) {
	t.Parallel()
	const tpm = `{"error": {"message": "Request too large for model openai/gpt-oss-120b in organization org_1 service tier on_demand on tokens per minute (TPM): Limit 8000, Requested 15234, please reduce your message size and try again.", "type": "tokens", "code": "rate_limit_exceeded"}}`
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			switch body["model"] {
			case "gpt-oss":
				return 413, "application/json", tpm
			case "huge":
				return 413, "text/html", "<html><body>413 Request Entity Too Large</body></html>"
			case "busy":
				return 429, "application/json", `{"error": {"message": "rate limited"}}`
			case "daily":
				return 413, "application/json", `{"error": {"message": "Daily quota of requests used up", "code": "rate_limit_exceeded"}}`
			}
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	for _, m := range []string{"gpt-oss", "huge", "busy", "daily", "fact-model"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text"}, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/gpt-oss", "groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	if got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "one"}), "done", "error"); got.State != "done" {
		t.Fatalf("the next model takes a request past the limit per minute: %+v %+v", got, e.Fault(got.ID))
	}
	if h := e.HealthRowOf("model:groq/gpt-oss"); h != nil && h["status"] != "ok" {
		t.Fatalf("a request past the limit per minute leaves the model up for smaller ones: %v", h)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/busy", "groq/fact-model"}}, nil)
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "busy"}), "done", "error")
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
	if i := slices.IndexFunc(m.Assignments, func(a harness.AssignmentOut) bool { return a.Role == "text" }); i < 0 || m.Assignments[i].Using != "groq/fact-model" ||
		!strings.HasPrefix(m.Assignments[i].Why, "groq/busy: answered HTTP 429") {
		t.Fatalf("the assignment says in one line of health why the first model does not take the work: %+v", m.Assignments)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/gpt-oss"}}, nil)
	if got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "two"}), "done", "error").ID); got.ErrorType != "model.limit" || got.Whose != "model" {
		t.Fatalf("the last model past its limit per minute is the model's limit: %+v", got)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/daily"}}, nil)
	if got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "daily"}), "done", "error").ID); got.ErrorType != "model.limit" {
		t.Fatalf("a 413 past another rate is the model's limit: %+v", got)
	}
	if h := e.HealthRowOf("model:groq/daily"); h == nil || h["status"] != "error" {
		t.Fatalf("a model past a rate other than per minute is red: %v", h)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/huge", "groq/fact-model"}}, nil)
	if got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "three"}), "done", "error").ID); got.ErrorType != "input.invalid" {
		t.Fatalf("a request too big for the model at all is the input's and goes to no other model: %+v", got)
	}
}

// TestARequestPastTheLimitsOfAModelGoesToTheNextAtOnce: no request past the limits of a model reaches it; the next
// model takes it, the last is the model's limit, and an assignment warns when a usual request is past them.
func TestARequestPastTheLimitsOfAModelGoesToTheNextAtOnce(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "small", "kind": "text", "tpm": 20}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "fact-model", "kind": "text", "context": 100000}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "small", "call_usd": 0.001}, nil)
	var m struct {
		Models []struct {
			Name         string
			TPM, Context int64
		}
	}
	e.Must("deck.models_list", nil, &m)
	if i := slices.IndexFunc(m.Models, func(x struct {
		Name         string
		TPM, Context int64
	}) bool {
		return x.Name == "groq/small"
	}); i < 0 || m.Models[i].TPM != 20 {
		t.Fatalf("the models list gives the limits, kept by a change without them: %+v", m.Models)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/small", "groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	if got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "one"}), "done", "error"); got.State != "done" {
		t.Fatalf("the next model takes a request past the limit: %+v", e.Fault(got.ID))
	}
	for _, c := range api.Calls() {
		if c.Model == "small" {
			t.Fatalf("no request past its limit reaches the model: %+v", c)
		}
	}
	var set struct{ Warnings []string }
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "command": "factcheck", "models": []string{"groq/small"}}, &set)
	if len(set.Warnings) != 1 || !strings.Contains(set.Warnings[0], "groq/small takes 20 tokens a minute") {
		t.Fatalf("the assignment warns that a usual request of the work is past the limit: %+v", set)
	}
	if got := e.Fault(e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "two"}), "done", "error").ID); got.ErrorType != "model.limit" {
		t.Fatalf("a request past the limit of the last model is the model's limit: %+v", got)
	}
}

package owner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestAModelPickerOffersTheModelsOfItsWorkWithTheirMarkAndNote: a picker of models offers only those of its work,
// each with the words of the model's own mark, under its provider, and a note of its price and context.
func TestAModelPickerOffersTheModelsOfItsWorkWithTheirMarkAndNote(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": "http://127.0.0.1:1/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "ollama", "model": "qwen3:8b", "name": "local-qwen", "kind": "text", "context": 131072}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "ollama", "model": "kokoro", "name": "local-voice", "kind": "tts"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "http://127.0.0.1:1/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "xiaomi/mimo-v2-flash:free", "name": "openrouter-mimo-v2-flash-free",
		"kind": "text", "free": true}, nil)

	var list struct {
		Models []struct {
			Name     string   `json:"name"`
			Roles    []string `json:"roles"`
			Note     string   `json:"note"`
			Logo     string   `json:"logo"`
			Provider string   `json:"provider"`
		} `json:"models"`
	}
	e.Must("deck.models_list", nil, &list)
	byName := map[string]int{}
	for i, m := range list.Models {
		byName[m.Name] = i
	}
	qwen, voice, mimo := list.Models[byName["local-qwen"]], list.Models[byName["local-voice"]], list.Models[byName["openrouter-mimo-v2-flash-free"]]
	if !slices.Equal(qwen.Roles, []string{"text"}) || qwen.Logo != "qwen3:8b" || qwen.Provider != "ollama" || qwen.Note != "free · context 131K" {
		t.Fatalf("a model says the work it takes, the words of its own mark, its provider and its note: %+v", qwen)
	}
	if mimo.Logo != "xiaomi/mimo-v2-flash:free" || mimo.Provider != "openrouter" {
		t.Fatalf("a model through OpenRouter wears its maker's mark, OpenRouter is its provider: %+v", mimo)
	}
	if !slices.Equal(voice.Roles, []string{"tts"}) || voice.Note != "free" {
		t.Fatalf("a voice takes only the voice: %+v", voice)
	}
	for _, m := range list.Models {
		if m.Name == "claude-code" && !slices.Contains(m.Roles, "agent") {
			t.Fatalf("an agent takes the agent work: %+v", m)
		}
	}

	choices := func(params string) map[string]map[string]any {
		var fv struct {
			Choices map[string][]map[string]any `json:"choices"`
		}
		_ = json.Unmarshal(e.Screen("", "settings", map[string]any{"params": params, "dialog": "assign"})[formID(t, e, "deck.assignment_set")]["value"], &fv)
		out := map[string]map[string]any{}
		for _, c := range fv.Choices["models"] {
			out[c["value"].(string)] = c
		}
		return out
	}
	tts := choices(`{"role": "tts"}`)
	if len(tts) != 1 || tts["local-voice"] == nil || tts["local-voice"]["note"] != "free" || tts["local-voice"]["logo"] != "kokoro" || tts["local-voice"]["group"] != "ollama" {
		t.Fatalf("the fallbacks of the voice offer only voices, each with its note and mark under its provider: %+v", tts)
	}
	if all := choices(`{}`); all["local-voice"] == nil || all["local-qwen"] == nil || all["openrouter-mimo-v2-flash-free"]["group"] != "openrouter" {
		t.Fatalf("without a work every model is offered, under its provider: %+v", all)
	}

	e.Must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"local-voice"}}, nil)
	pickers := 0
	for _, it := range harness.BlockData[[]harness.ListItem](e.T, e.SettingsBlock("Who does what", nil)["items"]) {
		for _, a := range it.Actions {
			if a["verb"] != "model" {
				continue
			}
			pickers++
			opts, _ := a["options"].([]any)
			logos, _ := a["logos"].([]any)
			notes, _ := a["notes"].([]any)
			if len(opts) == 0 || len(logos) != len(opts) || len(notes) != len(opts) {
				t.Fatalf("the picker of the model of %q gives the mark and note of each option: %+v", it.Title, a)
			}
			for i, o := range opts {
				if o == "local-voice" && notes[i] != "free" {
					t.Fatalf("the note of a model in the picker is its note: %+v", a)
				}
			}
		}
	}
	if pickers == 0 {
		t.Fatal("Who does what has pickers of models")
	}
}

// TestTheModelsOfSpendWearTheMarkOfTheirProvider: a row of spend by model wears the mark of its provider, then model.
func TestTheModelsOfSpendWearTheMarkOfTheirProvider(t *testing.T) {
	t.Parallel()
	e := harness.LightEnv(t)
	var rows []struct {
		Cells []any `json:"cells"`
		Logos []any `json:"logos"`
	}
	_ = json.Unmarshal(e.ScreenBlock("", "spend", "By model", nil)["rows"], &rows)
	if len(rows) == 0 {
		t.Fatal("a working week has spend by model")
	}
	for _, r := range rows {
		if len(r.Logos) == 0 || r.Logos[0] == nil || !strings.HasSuffix(r.Logos[0].(string), r.Cells[0].(string)) {
			t.Fatalf("the model of a row wears the mark of its provider: %+v", r)
		}
	}
}

// TestANewExpressionOfMarksAndNotesIsCheckedOnSave: the logo of a column, the notes and logos of a button and the
// note, logo and icon of a choice are expressions a save compiles.
func TestANewExpressionOfMarksAndNotesIsCheckedOnSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, c := range []struct{ from, to, want string }{
		{`"columns": [{"label": "Title", "bind": "title"}]`, `"columns": [{"label": "Title", "bind": "title", "logo": "title +"}]`, `column "Title" logo`},
		{`{"label": "Open", "screen": "note"}]}]}`, `{"label": "Open", "screen": "note"}, {"label": "Tag", "command": "tag", "input": "tag", "options": "=['a']", "notes": "=[1 +"}]}]}`, "notes: "},
		{`{"type": "form", "title": "Score a title", "command": "score"}`,
			`{"type": "form", "title": "Score a title", "command": "score", "choices": {"title": {"source": {"collection": "notes"}, "value": "title", "logo": "title +"}}}`, "choices"},
	} {
		bad := strings.Replace(harness.WebNotes, c.from, c.to, 1)
		if bad == harness.WebNotes {
			t.Fatalf("the manifest has no %s", c.from)
		}
		e.Refused("deck.project_create", map[string]any{"name": "bad", "manifest": bad}, 422, c.want)
	}
}

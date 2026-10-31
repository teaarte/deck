package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestTheChangesOfARunAreOneLine: what a run of a module deleted is one line of done without you with how many, in
// the owner's words, and its undo writes every record back at once; another run is another line.
func TestTheChangesOfARunAreOneLine(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		list, _ := c.Tool(ctx, "records_list", map[string]any{"project": "notes", "collection": "ideas"})
		var recs []struct{ ID int64 }
		_ = json.Unmarshal([]byte(list), &recs)
		for _, r := range recs[:min(3, len(recs))] {
			c.Tool(ctx, "record_delete", map[string]any{"record": r.ID})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	e.Project("notes", ideasManifest)
	for i := range 4 {
		e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": fmt.Sprint("idea ", i), "score": i})}, nil)
	}
	e.WaitState(e.Start("notes", "collect", nil), "done")
	e.WaitState(e.Start("notes", "collect", nil), "done")
	type line struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	var feed []line
	e.Must("deck.feed_list", map[string]any{"kind": "change", "lang": "ru"}, &feed)
	if len(feed) != 2 || feed[1].Text != "удалено записей из ideas: 3" || !feed[1].CanUndo || !strings.HasPrefix(feed[0].Text, "удалено idea") {
		t.Fatalf("a run is one line with how many in the owner's words: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[1].ID}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "notes", "collection": "ideas"}, &recs)
	if len(recs) != 3 {
		t.Fatalf("the undo of the line writes back all it deleted: %+v", recs)
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if feed[1].CanUndo || !feed[0].CanUndo {
		t.Fatalf("the line is undone, the other one not: %+v", feed)
	}
}

// TestAManifestCheckOfAProjectAloneChecksItsCurrentManifest: manifest_check with only a project checks the manifest
// in force; data, tickers, numbers and signs want no translation, and words its filters and expressions show are used.
func TestAManifestCheckOfAProjectAloneChecksItsCurrentManifest(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(ideasManifest, `"screens": [{
    "name": "main", "title": "Ideas",`, `"i18n": {"ru": {"Ideas": "Идеи", "Best ideas": "Лучшие идеи", "Idea": "Идея", "Score": "Оценка", "Again": "Ещё раз",
      "Collect ideas": "Собрать идеи", "Writes ideas": "Пишет идеи", "Short ideas": "Короткие идеи", "title": "название", "score": "оценка",
      "topic": "тема", "written": "записано", "fresh": "свежая", "high": "высокая", "Level": "Уровень", "rare": "редко"}},
  "screens": [{
    "name": "main", "title": "Ideas", "params": {"level": "string", "topic": "string"},
    "side": [{"type": "filter", "title": "Level", "param": "level", "options": ["high", "BTC"]},
      {"type": "filter", "param": "topic", "source": {"collection": "ideas"}, "field": "topic"}],`, 1)
	m = strings.Replace(m, `{"label": "Score", "bind": "score"}`, `{"label": "Score", "bind": "score > 8 ? 'fresh' : '$0'", "format": "tag"}, {"label": "R:R", "bind": "score"}`, 1)
	e.Project("notes", m)
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "a", "score": 9, "topic": "Биткоин"}, map[string]any{"title": "b", "score": 1, "topic": "ETH"}, map[string]any{"title": "c", "score": 2, "topic": "rare"})}, nil)
	var c struct {
		Ok           bool
		Refused      []string
		Unused       []string `json:"unused_i18n"`
		Untranslated []string
	}
	e.Must("deck.manifest_check", map[string]any{"project": "notes"}, &c)
	if !c.Ok || len(c.Untranslated) != 0 || !slices.Equal(c.Unused, []string{}) {
		t.Fatalf("a check of the current manifest says nothing false of its words: %+v", c)
	}
}

// TestNoSchemaAnAgentSeesHasAllows: the field a press of the owner gives the calls its button allows in is in no input
// schema of a command, and only the owner's call gives it.
func TestNoSchemaAnAgentSeesHasAllows(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", ideasManifest)
	var d struct{ Input json.RawMessage }
	e.Must("deck.command_describe", map[string]any{"command": "notes.collect"}, &d)
	if strings.Contains(string(d.Input), "allows") || !strings.Contains(string(d.Input), "retry_key") {
		t.Fatalf("the schema of a command does not name allows: %s", d.Input)
	}
	_, tok := e.Token("terminal", "notes.*")
	if code, out := e.As(tok).Call("notes.collect", map[string]any{"allows": "[]"}); code != 422 || !strings.Contains(string(out), "allows") {
		t.Fatalf("allows from another than the owner is refused: %d %s", code, out)
	}
	if code, out := e.Call("notes.collect", map[string]any{"allows": `[{"command": "deck.notify", "params": {}}]`}); code != 200 {
		t.Fatalf("the owner's call gives allows: %d %s", code, out)
	}
}

// TestAManifestTheMasterChangedTwiceInARunGoesBackOnce: two saves of a manifest in one reply of the master are one
// line, undone to the manifest before the first; one the owner changed between them is not undone over their change.
func TestAManifestTheMasterChangedTwiceInARunGoesBackOnce(t *testing.T) {
	t.Parallel()
	plans, mid, on := make(chan bool, 1), make(chan bool), make(chan bool)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var owner bool
		select {
		case owner = <-plans:
		default:
			return provider.Result{Text: "ok"}
		}
		for _, edit := range [][2]string{{`"title": "Work"`, `"title": "Work hard"`}, {`"title": "Work hard"`, `"title": "Work harder"`}} {
			c.Tool(ctx, "manifest_save", map[string]any{"project": "notes", "old": []string{edit[0]}, "new": []string{edit[1]}})
			if owner && edit[1] == `"title": "Work hard"` {
				mid <- true
				<-on
			}
		}
		return provider.Result{Text: "Saved."}
	}))

	e.Project("notes", harness.PlainManifest)
	var feed []struct {
		ID   int64
		Text string
	}
	title := func() string {
		var cur struct{ Manifest string }
		e.Must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
		var m struct{ Commands []struct{ Title string } }
		_ = json.Unmarshal([]byte(cur.Manifest), &m)
		return m.Commands[0].Title
	}

	plans <- false
	e.MasterSaid(e.Say(0, "retitle the work"), 1)
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || feed[0].Text != "changed the manifest of notes ×2" {
		t.Fatalf("two saves of a run are one line: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := title(); got != "Work" {
		t.Fatalf("the undo goes back to before the first save: %q", got)
	}

	plans <- true
	topic := e.Say(0, "retitle the work again")
	<-mid
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"description": "Does work"`}, "new": []string{`"description": "Does the owner's work"`}}, nil)
	on <- true
	e.MasterSaid(topic, 1)
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	e.Refused("deck.feed_undo", map[string]any{"entry": feed[0].ID}, 409, "changed after")
}

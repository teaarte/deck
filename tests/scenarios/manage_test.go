package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestTodayHealthAndTheScheduleSayOneThing: a schedule that failed is one failure on Today, on Health with its
// count and on top of the Schedule, each with its way to run it again; closed once, it is red nowhere.
func TestTodayHealthAndTheScheduleSayOneThing(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is down"}
	}))

	e.Project("digest", testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	var ts []struct {
		ID           int64
		Title        string
		ProjectTitle string `json:"project_title"`
		NextAt       string `json:"next_at"`
		LastAt       string `json:"last_at"`
		Failure      string
		LastTone     string `json:"last_tone"`
		LastProcess  int64  `json:"last_process"`
	}
	read := func() {
		ts = nil
		e.Must("deck.triggers_list", map[string]any{"project": "digest", "lang": "ru"}, &ts)
	}
	read()
	if len(ts) != 1 || ts[0].Title != "work" || ts[0].ProjectTitle != "digest" || ts[0].NextAt == "" || ts[0].LastAt != "" {
		t.Fatalf("the schedule says what runs, of which module, and when next: %+v", ts)
	}
	e.Must("deck.trigger_run", map[string]any{"trigger": ts[0].ID}, nil)
	read()
	e.WaitState(ts[0].LastProcess, "error")
	read()
	if ts[0].Failure == "" || ts[0].LastTone != "error" || ts[0].LastAt == "" {
		t.Fatalf("a failed last run is a failure of the schedule with when it ran: %+v", ts)
	}
	if h := e.today(""); h.Failed != 1 {
		t.Fatalf("Today counts the failure: %+v", h)
	}
	broken := blockData[[]listItem](t, e.settingsBlock("Broken now", nil)["items"])
	if len(broken) != 1 || broken[0].Text != ts[0].Failure && !strings.Contains(broken[0].Text, "the model is down") || !hasLabel(broken[0].Actions, "Run again") {
		t.Fatalf("Health says the same failure with a way to run it again: %+v", broken)
	}
	counted := false
	for _, m := range e.screenMessages("", "settings", nil) {
		counted = counted || strings.Contains(string(m["updateComponents"]), `"count":"failures"`)
	}
	if !counted {
		t.Fatal("the tab of Health counts the failures")
	}
	top := blockData[[]listItem](t, e.screenBlock("", "schedule", "Did not pass", nil)["items"])
	if len(top) != 1 || !strings.Contains(top[0].Title, "work") || !hasLabel(top[0].Actions, "Run again") {
		t.Fatalf("the schedule says on top what did not pass: %+v", top)
	}
	rows := blockData[[]screenRow](t, e.screenBlock("", "schedule", "What runs by itself", nil)["rows"])
	rows = slices.DeleteFunc(rows, func(r screenRow) bool { return !strings.HasPrefix(fmt.Sprint(r.Cells[0]), "work") })
	if len(rows) != 1 || rows[0].Cells[3] != "see above" || rows[0].Tones[3] != "error" {
		t.Fatalf("the row of the schedule points up instead of saying it twice: %+v", rows)
	}

	var got []struct{ Label, Command string }
	raw, _ := json.Marshal(broken[0].Actions)
	_ = json.Unmarshal(raw, &got)
	i := slices.IndexFunc(got, func(a struct{ Label, Command string }) bool { return a.Label == "Got it" })
	if i < 0 || got[i].Command != "deck.process_dismiss" {
		t.Fatalf("got it closes the failure: %+v", got)
	}
	e.Must("deck.process_dismiss", map[string]any{"process": ts[0].LastProcess}, nil)
	read()
	if h := e.today(""); h.Failed != 0 || ts[0].Failure != "" || ts[0].LastTone == "error" {
		t.Fatalf("a closed failure is red nowhere: %+v %+v", h, ts)
	}
	if broken := blockData[[]listItem](t, e.settingsBlock("Broken now", nil)["items"]); len(broken) != 0 {
		t.Fatalf("Health is quiet again: %+v", broken)
	}
}

func hasLabel(acts []map[string]any, label string) bool {
	return slices.ContainsFunc(acts, func(a map[string]any) bool { return a["label"] == label })
}

// TestTodayTakesTheButtonsOfADecisionFromItsRow: the buttons of what waits are said once, by asks_list, and Today
// shows those, as the chat does.
func TestTodayTakesTheButtonsOfADecisionFromItsRow(t *testing.T) {
	t.Parallel()
	e := New(t)
	_, tok := e.token("asker", "deck.model_set")
	if code, out := e.As(tok).Call("deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	var asks []struct{ Actions []map[string]any }
	e.Must("deck.asks_list", map[string]any{"lang": "ru"}, &asks)
	if len(asks) != 1 || len(asks[0].Actions) < 3 {
		t.Fatalf("a decision carries its buttons: %+v", asks)
	}
	items := blockData[[]listItem](t, e.screenData("", "cockpit", map[string]any{"lang": "ru"})["b0"]["items"])
	verbs := func(acts []map[string]any) []string {
		var out []string
		for _, a := range acts {
			out = append(out, fmt.Sprint(a["verb"], a["label"], a["hold"]))
		}
		return out
	}
	if len(items) != 1 || !slices.Equal(verbs(items[0].Actions), verbs(asks[0].Actions)) {
		t.Fatalf("Today shows the buttons of the row: %v, the row %v", verbs(items[0].Actions), verbs(asks[0].Actions))
	}
	own := `{"version": 13, "collections": [{"name": "notes", "description": "Notes.", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "list", "source": {"collection": "notes"}, "item": {"title": "title", "actions": "actions"}}]}]}`
	if code, out := e.Call("deck.manifest_check", map[string]any{"manifest": own}); code != 200 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a module names its buttons itself: %d %s", code, out)
	}
}

// TestTheFileManagerHoldsEveryModuleInOnePlace: Files opens the folders of the modules, in each its files, documents
// and data folder, by steps of its own; the master is a folder with what it holds, and a search finds a file by name.
func TestTheFileManagerHoldsEveryModuleInOnePlace(t *testing.T) {
	t.Parallel()
	e := New(t)
	p := e.Project("inbox", filesManifest())
	e.upload("inbox", "invoice-october.pdf", "application/pdf", "%PDF-1.4")
	e.upload("inbox", "notes.txt", "text/plain", "hello")
	data := e.App.Home.Data(p)
	if err := errors.Join(os.MkdirAll(filepath.Join(data, "cache", "empty"), 0o700), os.WriteFile(filepath.Join(data, "cache", "seen.json"), []byte("{}"), 0o600)); err != nil {
		t.Fatal(err)
	}
	type line struct {
		ID         any
		Title      string
		Meta, Icon string
		Actions    []map[string]any
	}
	type crumb struct{ Label string }
	at := func(path string) ([]line, []crumb, string) {
		b := e.screenBlock("", "files", "Modules", map[string]any{"lang": "ru", "params": fmt.Sprintf(`{"path": %q}`, path)})
		var ls []line
		var cs []crumb
		_ = json.Unmarshal(b["files"], &ls)
		_ = json.Unmarshal(b["crumbs"], &cs)
		var state string
		_ = json.Unmarshal(b["state"], &state)
		return ls, cs, state
	}
	root, _, _ := at("")
	i := slices.IndexFunc(root, func(l line) bool { return l.Title == "inbox" })
	m := slices.IndexFunc(root, func(l line) bool { return l.Title == "Деки" || l.Title == "master" })
	if i < 0 || !strings.Contains(root[i].Meta, "файлов: 2") || m < 0 || !strings.Contains(root[m].Meta, "Память Деки") {
		t.Fatalf("the root holds the folders of the modules, the master's says what it holds: %+v", root)
	}
	inside, crumbs, _ := at("inbox")
	names := []string{}
	for _, l := range inside {
		names = append(names, l.Title)
	}
	if !slices.Equal(names, []string{"Файлы", "Документы", "Папка данных"}) || len(crumbs) != 2 || crumbs[1].Label != "inbox" {
		t.Fatalf("a module holds its files, documents and data folder, its step named as the module: %v %+v", names, crumbs)
	}
	files, crumbs, _ := at("inbox/files")
	if len(files) != 2 || len(crumbs) != 3 || crumbs[2].Label != "Файлы" {
		t.Fatalf("its files, the steps by their names: %+v %+v", files, crumbs)
	}
	docs, _, _ := at("inbox/documents")
	d := slices.IndexFunc(docs, func(l line) bool { return l.Title == "manifest.json" })
	if d < 0 || !slices.ContainsFunc(docs[d].Actions, func(a map[string]any) bool { return a["screen"] == "document" && a["project"] == "inbox" }) {
		t.Fatalf("a document opens on its page in the module: %+v", docs)
	}
	folder, crumbs, _ := at("inbox/data/cache")
	if len(folder) != 2 || len(crumbs) != 4 || crumbs[3].Label != "cache" || slices.ContainsFunc(folder, func(l line) bool { return l.Title == ".." }) {
		t.Fatalf("the data folder by its folders, the way up by its steps alone: %+v %+v", folder, crumbs)
	}
	if _, crumbs, state := at("inbox/data/cache/empty"); state != "empty" || len(crumbs) != 5 {
		t.Fatalf("an empty folder keeps its steps back: %s %+v", state, crumbs)
	}
	id, _ := e.findBlock("", "files", "All files")
	found := e.screenData("", "files", map[string]any{"filter": `{"` + id + `": {"~": "invoice oct"}}`})[id]
	var hits []line
	_ = json.Unmarshal(found["files"], &hits)
	if len(hits) != 1 || hits[0].Title != "invoice-october.pdf" {
		t.Fatalf("the search finds a file by the words of its name: %+v", hits)
	}
	var all []struct{ Name string }
	e.Must("deck.files_list", map[string]any{"search": "INVOICE pdf"}, &all)
	if len(all) != 1 || all[0].Name != "invoice-october.pdf" {
		t.Fatalf("files_list looks for the words through every file: %+v", all)
	}
	if mod := e.screenData("inbox", "documents", map[string]any{"params": `{"path": "files"}`})["b0"]; !strings.Contains(string(mod["files"]), "invoice-october.pdf") {
		t.Fatalf("the files of a module are the same manager inside it: %s", mod["files"])
	}
}

// TestAManifestIsCheckedWithoutASave: manifest_check says what a save would refuse, warn of and widen, and saves
// nothing; a button that only opens the chat is refused, as the button of Deki opens it.
func TestAManifestIsCheckedWithoutASave(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("web", webNotes)
	var before struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": "web"}, &before)
	chat := strings.Replace(webNotes, `"screens": [`, `"screens": [{"name": "inbox", "title": "Inbox", "blocks": [{"type": "list", "source": {"collection": "notes"},
    "item": {"title": "title"}, "actions": [{"label": "New note", "chat": true}]}]},`, 1)
	var c struct {
		Ok       bool
		Refused  []string
		Widens   []string
		Warnings []string
	}
	e.Must("deck.manifest_check", map[string]any{"project": "web", "manifest": chat}, &c)
	if c.Ok || !slices.ContainsFunc(c.Refused, func(r string) bool { return strings.Contains(r, "button of Deki") }) {
		t.Fatalf("a chat button is refused by the check: %+v", c)
	}
	wider := strings.Replace(webNotes, `{"version": 13,`, `{"version": 13, "domains": {"read": ["example.com"]},`, 1)
	e.Must("deck.manifest_check", map[string]any{"project": "web", "manifest": wider}, &c)
	if !c.Ok || len(c.Widens) == 0 {
		t.Fatalf("the check says what a manifest widens: %+v", c)
	}
	e.Must("deck.manifest_check", map[string]any{"manifest": `{"version": 13, "commands": [{"name": "x"}]}`}, &c)
	if c.Ok || len(c.Refused) == 0 {
		t.Fatalf("a manifest alone is checked too: %+v", c)
	}
	var after struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": "web"}, &after)
	if after.Revision != before.Revision {
		t.Fatalf("a check saves nothing: %d → %d", before.Revision, after.Revision)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": chat}, 422, "button of Deki")
}

// TestTheModulesOfSettingsAreCardsWithTheCoverOfTheTheme: a module in Settings and in Files shows the cover its card
// wears in the theme in use, with buttons to draw another and to take it off.
func TestTheModulesOfSettingsAreCardsWithTheCoverOfTheTheme(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("notes", filesManifest())
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{30, 120, 220, 255}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	e.Must("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(e.upload("notes", "blue.png", "image/png", b.String()))}, nil)
	var m struct {
		Projects []struct{ Project, Cover string }
	}
	e.Must("deck.menu", nil, &m)
	type card struct {
		Title, Text, Meta string
		Cover             map[string]string
		Actions           []map[string]any
	}
	var cards []card
	_ = json.Unmarshal(e.settingsBlock("Modules", nil)["items"], &cards)
	i := slices.IndexFunc(cards, func(c card) bool { return c.Title == "notes" })
	if m := slices.IndexFunc(cards, func(c card) bool { return c.Title != "notes" }); m < 0 || cards[m].Cover["light"] != "/deki/chill.webp" || cards[i].Text != "" || cards[i].Meta != "" {
		t.Fatalf("a card of a module says only its name, the master's wears Deki: %+v", cards)
	}
	if i < 0 || len(m.Projects) != 1 || cards[i].Cover["light"] != m.Projects[0].Cover || !strings.HasPrefix(cards[i].Cover["color"], "#") {
		t.Fatalf("the card of the module wears the cover of its tile: %+v %+v", cards, m)
	}
	if !hasLabel(cards[i].Actions, "Draw a cover") || !hasLabel(cards[i].Actions, "Take off the cover") || !slices.ContainsFunc(cards[i].Actions, func(a map[string]any) bool { return a["verb"] == "pause" }) {
		t.Fatalf("a module card pauses, draws its cover anew and takes it off: %+v", cards[i].Actions)
	}
	var files []card
	_ = json.Unmarshal(e.screenBlock("", "files", "Modules", nil)["files"], &files)
	if j := slices.IndexFunc(files, func(c card) bool { return c.Title == "notes" }); j < 0 || files[j].Cover["light"] != m.Projects[0].Cover {
		t.Fatalf("the folder of the module wears its cover too: %+v", files)
	}
	foreign := strings.Replace(filesManifest(), `"commands": [`, `"screens": [{"name": "wall", "title": "Wall", "blocks": [{"type": "list", "source": {"collection": "items"},
    "item": {"title": "name", "cover": "{'light': 'https://example.com/x.png'}"}}]}], "commands": [`, 1)
	e.Project("wall", foreign)
	e.Must("deck.records_upsert", map[string]any{"project": "wall", "collection": "items", "records": jsonl(map[string]any{"name": "a"})}, nil)
	if wall := e.screenData("wall", "wall", nil)["b0"]; strings.Contains(string(wall["items"]), "example.com") {
		t.Fatalf("a screen of a module names no address of a cover: %s", wall["items"])
	}
	off := cards[i].Actions[slices.IndexFunc(cards[i].Actions, func(a map[string]any) bool { return a["label"] == "Take off the cover" })]
	e.Must(fmt.Sprint(off["command"]), off["params"], nil)
	cards = nil
	_ = json.Unmarshal(e.settingsBlock("Modules", nil)["items"], &cards)
	if i := slices.IndexFunc(cards, func(c card) bool { return c.Title == "notes" }); cards[i].Cover != nil {
		t.Fatalf("a cover taken off is gone from the card: %+v", cards[i])
	}
}

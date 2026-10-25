package scenarios

import (
	"context"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type documentV struct {
	ID        int64          `json:"id"`
	Project   string         `json:"project"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Text      string         `json:"text"`
	Revision  int64          `json:"revision"`
	Editable  bool           `json:"editable"`
	Record    int64          `json:"record"`
	Save      map[string]any `json:"save"`
	Revisions []struct {
		Revision int64          `json:"revision"`
		Author   string         `json:"author"`
		Reason   string         `json:"reason"`
		Revert   map[string]any `json:"revert"`
	} `json:"revisions"`
}

// docsManifest is the web project with a document block on its main screen and one of the record on its page,
// a list and a card with a field in markdown.
var docsManifest = strings.NewReplacer(
	`{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},`, `{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},
      {"type": "document", "title": "Criteria", "document": "notes/criteria.md"},
      {"type": "list", "title": "Posts", "source": {"collection": "notes"}, "item": {"title": "title", "text": "note"}},`,
	`{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`, `{"type": "form", "title": "Edit", "command": "deck.record_update"},
      {"type": "card", "fields": [{"label": "Note", "bind": "note", "markdown": true}, {"label": "Title", "bind": "title"}]},
      {"type": "document", "title": "Report", "document": "report.md"}]}`,
).Replace(webNotes)

// TestDocumentsOfAProjectAreListedOnItsDocumentsScreenAndEditedOnTheirPage: every project gets a documents
// screen; a document page shows the text and revisions, and only md documents are edited on screen.
func TestDocumentsOfAProjectAreListedOnItsDocumentsScreenAndEditedOnTheirPage(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("web", docsManifest)
	e.createProject("other", webNotes)
	save := func(name, kind, text string, extra map[string]any) (d struct{ ID int64 }) {
		in := map[string]any{"project": "web", "name": name, "kind": kind, "text": text}
		for k, v := range extra {
			in[k] = v
		}
		e.must("deck.document_save", in, &d)
		return d
	}
	save("notes/criteria.md", "doc", "# Criteria\n\nfirst", nil)
	crit := save("notes/criteria.md", "doc", "# Criteria\n\n- **short**", map[string]any{"reason": "tighter"})
	save("custom/chart.html", "custom", "<p>x</p>", nil)
	var recs struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "note": "**bold** note"})}, &recs)
	save("report.md", "doc", "report", map[string]any{"record": recs.IDs[0]})

	var menu []struct {
		Project string
		Screens []struct{ Name, Title string }
		Extra   []struct{ Name, Title, Icon string }
	}
	e.menuOf(&menu)
	if s, x := menu[1].Screens, menu[1].Extra; menu[1].Project != "other" && menu[2].Project != "web" || len(s) != 1 || len(x) != 2 || x[0].Name != "documents" || x[0].Title != "Files" || x[0].Icon != "folder" || x[1].Name != "settings" {
		t.Fatalf("each project menu has its own screens, its files and settings apart: %+v", menu)
	}
	check.messages(t, e.screenMessages("web", "documents", nil))
	type tileV struct {
		ID               any
		Title, Icon, Url string
		Actions          []map[string]any
	}
	root := blockData[[]tileV](t, e.screenData("web", "documents", nil)["b0"]["files"])
	if len(root) != 3 || verb(t, root[1].Actions, "open")["params"].(map[string]any)["path"] != "documents" {
		t.Fatalf("the files screen of a module holds its files, documents and data folder: %+v", root)
	}
	list := e.screenData("web", "documents", map[string]any{"params": `{"path": "documents"}`})["b0"]
	tiles := blockData[[]tileV](t, list["files"])
	byName := map[string]int{}
	for i, f := range tiles {
		byName[f.Title] = i
	}
	if len(tiles) != 4 || tiles[byName["manifest.json"]].Icon != "settings" || tiles[byName["notes/criteria.md"]].Icon != "file-text" ||
		tiles[byName["report.md"]].Url != "" || tiles[byName["custom/chart.html"]].Icon != "file-text" {
		t.Fatalf("the documents folder is a file manager of every document of the project, a tile of its kind each: %+v", tiles)
	}
	open := verb(t, tiles[byName["notes/criteria.md"]].Actions, "open")
	if open["screen"] != "document" || open["project"] != "web" || open["record"] != float64(crit.ID) || open["primary"] != true {
		t.Fatalf("a tile opens the page of its document: %+v", open)
	}

	page := func(id int64) documentV {
		t.Helper()
		msgs := e.screenMessages("web", "document", map[string]any{"record": id})
		check.messages(t, msgs)
		return blockData[documentV](t, e.screenData("web", "document", map[string]any{"record": id})["b0"]["value"])
	}
	d := page(crit.ID)
	if d.Text != "# Criteria\n\n- **short**" || d.Revision != 2 || !d.Editable || len(d.Revisions) != 2 || d.Revisions[0].Reason != "tighter" {
		t.Fatalf("a document page shows the current text and the revisions: %+v", d)
	}
	var title string
	for _, m := range e.screenMessages("web", "document", map[string]any{"record": crit.ID}) {
		if raw, ok := m["updateComponents"]; ok && strings.Contains(string(raw), `"Document: notes/criteria.md"`) {
			title = "ok"
		}
	}
	if title == "" {
		t.Fatal("the page is titled by the document name")
	}
	for _, name := range []string{"custom/chart.html", "manifest.json"} {
		id := int64(0)
		if i, ok := byName[name]; ok {
			id = int64(tiles[i].ID.(float64))
		}
		if page(id).Editable {
			t.Fatalf("%s is read only on screen", name)
		}
	}
	var ds []struct{ ID int64 }
	e.must("deck.documents_list", map[string]any{"project": "other"}, &ds)
	other := ds[0].ID
	if code, out := e.call("deck.screen", map[string]any{"project": "web", "screen": "document", "record": other}); code != 404 || !strings.Contains(string(out), "no document") {
		t.Fatalf("a page of one project does not show a document of another: %d %s", code, out)
	}

	events := e.sse(t.Context())
	d = page(crit.ID)
	if d.Revisions[0].Revert != nil || d.Revisions[1].Revert == nil || d.Save["command"] != "deck.document_save" {
		t.Fatalf("an editable document has its save, and each revision but the current its revert, from the server: %+v", d)
	}
	if code, out := e.press(d.Revisions[1].Revert); code != 200 {
		t.Fatalf("the revert of the server goes back to its revision: %d %s", code, out)
	}
	waitTopic(t, events, "documents")
	var got documentV
	for _, m := range e.screenMessages("web", "document", map[string]any{"record": crit.ID, "topic": "documents"}) {
		var dm struct{ Value struct{ Value documentV } }
		if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Value.Value.Name != "" {
			got = dm.Value.Value
		}
	}
	if got.Text != "# Criteria\n\nfirst" || got.Revision != 3 {
		t.Fatalf("going back to a revision is a new revision the page reads on the documents topic: %+v", got)
	}
}

// TestADocumentBlockOfAManifestShowsTheDocumentItNamesAndMarkdownIsOptIn: a document block names a document of
// the project, or on a record page one of the record; text is markdown only where a card field says so.
func TestADocumentBlockOfAManifestShowsTheDocumentItNamesAndMarkdownIsOptIn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("web", docsManifest)
	var recs struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "note": "**bold** note"})}, &recs)
	rec := recs.IDs[0]

	check.messages(t, e.screenMessages("web", "main", nil))
	main := e.screenData("web", "main", nil)
	if d := blockData[documentV](t, main["b1"]["value"]); d.ID != 0 || d.Name != "notes/criteria.md" || !d.Editable {
		t.Fatalf("a document not written yet shows empty and can be written: %+v", d)
	}
	e.must("deck.document_save", map[string]any{"project": "web", "name": "notes/criteria.md", "kind": "doc", "text": "be short"}, nil)
	if d := blockData[documentV](t, e.screenData("web", "main", nil)["b1"]["value"]); d.Text != "be short" || d.Revision != 1 {
		t.Fatalf("the block shows the document of the project it names: %+v", d)
	}
	items := blockData[[]map[string]any](t, main["b2"]["items"])
	if len(items) != 1 || items[0]["markdown"] != nil || items[0]["text"] != "**bold** note" {
		t.Fatalf("the text of a list stays text: %+v", items)
	}

	check.messages(t, e.screenMessages("web", "note", map[string]any{"record": rec}))
	note := e.screenData("web", "note", map[string]any{"record": rec})
	card := blockData[struct {
		Fields []map[string]any
	}](t, note["b1"]["value"])
	if card.Fields[0]["markdown"] != true || card.Fields[1]["markdown"] != nil {
		t.Fatalf("only the card field that says so is markdown: %+v", card.Fields)
	}
	if d := blockData[documentV](t, note["b2"]["value"]); d.ID != 0 || d.Record != rec {
		t.Fatalf("on a record page the block names a document of the record: %+v", d)
	}
	e.must("deck.document_save", map[string]any{"project": "web", "name": "report.md", "kind": "doc", "text": "report", "record": rec}, nil)
	if d := blockData[documentV](t, e.screenData("web", "note", map[string]any{"record": rec})["b2"]["value"]); d.Text != "report" || d.Record != rec {
		t.Fatalf("the record page shows the document of its record: %+v", d)
	}
	var got struct{ Text string }
	e.must("deck.document_get", map[string]any{"project": "web", "name": "report.md", "record": rec}, &got)
	if got.Text != "report" {
		t.Fatalf("document_get reads the document of a record by its name: %+v", got)
	}
	var listed []struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}
	e.must("deck.documents_list", map[string]any{"project": "web", "record": rec}, &listed)
	if len(listed) != 1 || listed[0].Name != "report.md" || listed[0].OwnerID != rec {
		t.Fatalf("documents_list gives the documents of a record: %+v", listed)
	}
	cockpit := e.cockpitItems()
	for _, it := range cockpit {
		if strings.Contains(it.Text, "**") {
			t.Fatalf("lists without markdown stay text: %+v", it)
		}
	}

	for _, tc := range []struct{ from, to, want string }{
		{`{"type": "document", "title": "Criteria", "document": "notes/criteria.md"}`, `{"type": "document", "title": "Criteria"}`, "names the document it shows"},
		{`{"label": "Title", "bind": "title"}], "actions"`, `{"label": "Title", "bind": "title", "markdown": true}], "actions"`, "additional properties 'markdown' not allowed"},
	} {
		if !strings.Contains(docsManifest, tc.from) {
			t.Fatalf("fixture lacks %s", tc.from)
		}
		code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(docsManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

func TestDocumentIDsAreNotReused(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	e.must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums", "records": jsonl(map[string]any{"n": 1})}, nil)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: e.projectID("nums"), Collection: "nums"})
	var old struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "nums", "name": "a.md", "kind": "artifact", "text": "mine", "record": recs[0].ID}, &old)
	e.must("deck.collection_delete", map[string]any{"project": "nums", "collection": "nums"}, nil)
	var fresh struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "nums", "name": "b.md", "kind": "doc", "text": "other"}, &fresh)
	if fresh.ID == old.ID {
		t.Fatalf("a new document must not take the id of a deleted one: %d", fresh.ID)
	}
	e.refused("deck.document_get", map[string]any{"document": old.ID}, 404, "no document")
}

// TestAShippedSkillEditedByTheMasterTakesTheNextVersionOfDeck: the master's edit of a built-in skill merges into the
// next text deck ships, and a conflict leaves deck's text with a note to the master; the owner's edit stays.
func TestAShippedSkillEditedByTheMasterTakesTheNextVersionOfDeck(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	const name = "skills/module-builder/SKILL.md"
	var d struct {
		ID   int64
		Text string
	}
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	shipped := d.Text
	if _, err := e.App.Store.DB.Exec(`INSERT INTO document_revision (document_id, n, text, author, reason)
		SELECT document_id, 0, 'the first skill of deck', 'deck', 'built-in skill' FROM document_revision WHERE document_id = ? AND n = 1`, d.ID); err != nil {
		t.Fatal(err)
	}
	e.must("deck.document_save", map[string]any{"name": name, "kind": "skill", "text": shipped + "\nThe master's own line.\n"}, nil)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET author = 'master' WHERE document_id = ? AND author = 'owner'`, d.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	if !strings.Contains(d.Text, "The master's own line.") {
		t.Fatal("an edit stays while deck ships the same skill")
	}
	first, _, _ := strings.Cut(shipped, "\n")
	older := strings.Replace(shipped, first, "# An older title", 1)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = CASE author WHEN 'deck' THEN ? ELSE ? END WHERE document_id = ?`,
		older, older+"\nThe master's own line.\n", d.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	if d.Text != shipped+"\nThe master's own line.\n" {
		t.Fatalf("a new version of deck keeps an edit of other lines: %.80q", d.Text)
	}
	var notes int
	if err := e.App.Store.DB.QueryRow(`SELECT count(*) FROM feed WHERE for_master = 1`).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("an edit carried over is no note to the master: %d %v", notes, err)
	}
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = 'an older skill' WHERE document_id = ? AND author = 'deck'`, d.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	if d.Text != shipped {
		t.Fatalf("a new version of deck replaces the edited skill: %.80q", d.Text)
	}
	var note string
	if err := e.App.Store.DB.QueryRow(`SELECT text FROM feed WHERE for_master = 1 ORDER BY id DESC LIMIT 1`).Scan(&note); err != nil || !strings.Contains(note, name) {
		t.Fatalf("the master is told its edit was replaced: %q %v", note, err)
	}
	e.must("deck.document_save", map[string]any{"name": name, "kind": "skill", "text": shipped + "\nThe owner's own line.\n"}, nil)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = 'an older skill' WHERE document_id = ? AND author = 'deck'`, d.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	if e.must("deck.document_get", map[string]any{"name": name}, &d); !strings.Contains(d.Text, "The owner's own line.") {
		t.Fatalf("an edit of the owner stays through a new deck: %.80q", d.Text)
	}
}

// TestAConflictOfAShippedSkillKeepsTheRestOfTheEditAndSendsTheDiff: where deck and the master changed the same lines of
// a built-in skill, deck's new text stands and the master gets the diff; the rest of the edit carries over.
func TestAConflictOfAShippedSkillKeepsTheRestOfTheEditAndSendsTheDiff(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	const name = "skills/module-builder/SKILL.md"
	var d struct {
		ID   int64
		Text string
	}
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	shipped := d.Text
	first := regexp.MustCompile(`(?m)^# .*$`).FindString(shipped)
	older := strings.Replace(shipped, first, "# An older title", 1)
	edit := strings.Replace(older, "# An older title", "# The master's title", 1) + "\nThe master's own line.\n"
	e.must("deck.document_save", map[string]any{"name": name, "kind": "skill", "text": edit}, nil)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET author = CASE author WHEN 'owner' THEN 'master' ELSE author END,
		text = CASE author WHEN 'deck' THEN ? ELSE text END WHERE document_id = ?`, older, d.ID); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	e.must("deck.document_get", map[string]any{"name": name}, &d)
	if d.Text != shipped+"\nThe master's own line.\n" {
		t.Fatalf("deck's text stands where both changed and the rest of the edit carries over: %.80q", d.Text)
	}
	var note string
	if err := e.App.Store.DB.QueryRow(`SELECT text FROM feed WHERE for_master = 1 ORDER BY id DESC LIMIT 1`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{name, "- # An older title", "+ # The master's title", "  " + first} {
		if !strings.Contains(note, want) {
			t.Fatalf("the note to the master holds the diff, %q missing: %s", want, note)
		}
	}
	if strings.Contains(note, "The master's own line.") {
		t.Fatalf("what carried over is not in the diff: %s", note)
	}
}

// commandWord is a command a skill or a prompt names: deck.<name>, or a tool name in backticks.
var commandWord = regexp.MustCompile("deck\\.([a-z]+(?:_[a-z]+)+)|`([a-z]+_(?:list|get|set|save|delete|remove|create|update|upsert|run|try|call|request|answer|describe|import|enable|pause|revert|restore|read|undo|send|add|check|format|stop|rework|log|key|subscribe|unsubscribe|revoke|archive|upload|move))`")

// TestSkillsAndPromptsNameOnlyCommandsDeckHas: an agent trusts its skills and prompts; a command they name that deck
// no longer has makes it report work it never did.
func TestSkillsAndPromptsNameOnlyCommandsDeckHas(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var cmds []struct{ Name string }
	e.must("deck.commands_list", nil, &cmds)
	have := map[string]bool{}
	for _, c := range cmds {
		have[strings.TrimPrefix(c.Name, "deck.")] = true
	}
	for _, root := range []string{"../../skills", "../../master"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range commandWord.FindAllStringSubmatch(string(b), -1) {
				if name := m[1] + m[2]; !have[name] {
					t.Errorf("%s names %s, which deck does not have", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestABuiltInSkillAskedInAProjectSaysItIsOfTheWorkspace: a skill deck ships is a document of the workspace; asked for
// in a project, the error says to leave out project.
func TestABuiltInSkillAskedInAProjectSaysItIsOfTheWorkspace(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	_, tok := e.token("terminal", "deck.*")
	code, out := e.callWith(tok, "deck.document_get", map[string]any{"project": "master", "name": "skills/module-builder/SKILL.md"})
	if code != 404 || !strings.Contains(string(out), "document of the workspace: leave out project") {
		t.Fatalf("the error says where the skill is: %d %s", code, out)
	}
	if code, out := e.callWith(tok, "deck.document_get", map[string]any{"project": "master", "name": "skills/none/SKILL.md"}); code != 404 || strings.Contains(string(out), "workspace") {
		t.Fatalf("a name missing everywhere is only missing: %d %s", code, out)
	}
	if code, _ := e.callWith(tok, "deck.document_get", map[string]any{"name": "skills/module-builder/SKILL.md"}); code != 200 {
		t.Fatalf("without project the skill reads: %d", code)
	}
}

// TestTheSkillNamesOnlyBlocksAndChartsTheCatalogHas: an agent builds screens from module-builder; a block or a kind of
// chart it names that the catalog lacks makes the save refuse what the skill taught.
func TestTheSkillNamesOnlyBlocksAndChartsTheCatalogHas(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var d struct{ Text string }
	e.must("deck.document_get", map[string]any{"name": "skills/module-builder/SKILL.md"}, &d)
	raw, err := os.ReadFile("../../schemas/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Components map[string]struct {
			Description string
			Block       struct {
				Properties struct {
					Kind struct{ Enum []string } `json:"kind"`
				} `json:"properties"`
			} `json:"block"`
		} `json:"components"`
		Blocks map[string]struct{ Description string } `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	quoted := regexp.MustCompile("`([a-z_]+)`")
	blocks := regexp.MustCompile("(?s)schema rule, such as .*?\\):(.*?)to lay them out").FindStringSubmatch(d.Text)
	kinds := regexp.MustCompile("(?s)`kind` is (.*?), a missing y").FindStringSubmatch(d.Text)
	if blocks == nil || kinds == nil {
		t.Fatal("the skill lists the blocks and the kinds of chart")
	}
	for _, m := range quoted.FindAllStringSubmatch(blocks[1], -1) {
		_, container := catalog.Blocks[m[1]]
		if _, ok := catalog.Components[m[1]]; !ok && !container {
			t.Errorf("the skill names block %q, which the catalog lacks", m[1])
		}
	}
	if strings.Contains(catalog.Components["chart"].Description, "area") {
		t.Errorf("the catalog describes a chart it does not draw: %s", catalog.Components["chart"].Description)
	}
	for _, m := range quoted.FindAllStringSubmatch(kinds[1], -1) {
		if !slices.Contains(catalog.Components["chart"].Block.Properties.Kind.Enum, m[1]) {
			t.Errorf("the skill names chart kind %q, which the catalog lacks", m[1])
		}
	}
}

// TestAScreenReadByAnotherShowsWhatItMayAndSaysWhatIsTheOwners: the master reading the files of deck and the
// documents of a project gets the documents without the deleted ones, and a block only the owner reads says so.
func TestAScreenReadByAnotherShowsWhatItMayAndSaysWhatIsTheOwners(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("plain", plainManifest)
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	for _, in := range []string{`{"screen": "files"}`, `{"project": "plain", "screen": "documents"}`} {
		out, err := e.App.Registry.Invoke(context.Background(), master, "deck.screen", []byte(in))
		if err != nil || strings.Contains(fmt.Sprint(out), "deleted documents") || strings.Contains(fmt.Sprint(out), `"state":"error"`) {
			t.Fatalf("a screen the master reads has no failed blocks: %s %v %v", in, out, err)
		}
	}
	out, _ := e.App.Registry.Invoke(context.Background(), master, "deck.screen", []byte(`{"screen": "files", "tab": "Snapshots"}`))
	if !strings.Contains(fmt.Sprint(out), "only the owner sees this") {
		t.Fatalf("the snapshots of the owner say whose they are: %v", out)
	}
}

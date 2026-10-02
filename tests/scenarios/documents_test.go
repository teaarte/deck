package scenarios

import (
	"context"
	"deck2/internal/store/db"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type documentV struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Revision  int64  `json:"revision"`
	Editable  bool   `json:"editable"`
	Record    int64  `json:"record"`
	Revisions []struct {
		Revision int64  `json:"revision"`
		Author   string `json:"author"`
		Reason   string `json:"reason"`
	} `json:"revisions"`
}

// docsManifest is the web project with a document block on its main screen and one of the record on its page,
// a list and a card in markdown.
var docsManifest = strings.NewReplacer(
	`{"type": "board", "title": "Work"},`, `{"type": "board", "title": "Work"}, {"type": "document", "title": "Criteria", "document": "notes/criteria.md"},
      {"type": "list", "title": "Posts", "source": {"collection": "notes"}, "item": {"title": "title", "text": "note", "markdown": true}},`,
	`{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`, `{"type": "form", "title": "Edit", "command": "deck.record_update"},
      {"type": "card", "fields": [{"label": "Note", "bind": "note", "markdown": true}, {"label": "Title", "bind": "title"}]},
      {"type": "document", "title": "Report", "document": "report.md"}]}`,
).Replace(boardWebManifest)

// TestDocumentsOfAProjectAreListedOnItsDocumentsScreenAndEditedOnTheirPage: every project gets a documents
// screen; a document page shows the text and revisions, and only md documents are edited on screen.
func TestDocumentsOfAProjectAreListedOnItsDocumentsScreenAndEditedOnTheirPage(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("web", docsManifest)
	e.createProject("other", boardWebManifest)
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
	}
	e.menuOf(&menu)
	if s := menu[1].Screens; menu[1].Project != "other" && menu[2].Project != "web" || len(s) != 3 || s[1].Name != "documents" || s[2].Name != "settings" {
		t.Fatalf("each project menu ends with its documents and its settings: %+v", menu)
	}
	check.messages(t, e.screenMessages("web", "documents", nil))
	list := e.screenData("web", "documents", nil)["b0"]
	rows := blockData[[]screenRow](t, list["rows"])
	byName := map[string]screenRow{}
	for _, r := range rows {
		byName[r.Cells[0].(string)] = r
	}
	if len(rows) != 4 || byName["notes/criteria.md"].Cells[3] != float64(2) || byName["manifest.json"].Cells[1] != "manifest" ||
		byName["report.md"].Cells[2] != fmt.Sprintf("record #%d", recs.IDs[0]) ||
		!strings.Contains(string(list["filters"]), `"custom"`) {
		t.Fatalf("the documents screen lists every document of the project with its kind and revision: %+v %s", rows, list["filters"])
	}
	open := verb(t, byName["notes/criteria.md"].Actions, "open")
	if open["screen"] != "document" || open["project"] != "web" || open["record"] != float64(crit.ID) {
		t.Fatalf("a row opens the page of its document: %+v", open)
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
		for _, r := range rows {
			if r.Cells[0] == name {
				id = int64(r.ID.(float64))
			}
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
	var reverted struct{ Revision int64 }
	e.must("deck.document_revert", map[string]any{"document": crit.ID, "revision": 1, "if_revision": 2}, &reverted)
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
// the project, or on a record page one of the record; text is markdown only where a list or card says so.
func TestADocumentBlockOfAManifestShowsTheDocumentItNamesAndMarkdownIsOptIn(t *testing.T) {
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
	if len(items) != 1 || items[0]["markdown"] != true || items[0]["text"] != "**bold** note" {
		t.Fatalf("a list says its text is markdown when its item does: %+v", items)
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

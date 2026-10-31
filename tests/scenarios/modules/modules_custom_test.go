package modules

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin: HTML, items and buttons come with the
// block; the second origin serves only the proxy page with a CSP without network.
func TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", harness.WebNotes)
	if b := e.Screen("web", "main", nil)["b2"]; harness.BlockData[string](t, b["error"]) != `no custom document "custom/chart.html"` {
		t.Fatalf("a custom block without its document shows why: %s", b["error"])
	}
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "b"}` + "\n" + `{"title": "a", "score": 2}`}, nil)
	v := harness.BlockData[struct {
		HTML    string           `json:"html"`
		Items   []map[string]any `json:"items"`
		Actions []map[string]any `json:"actions"`
	}](t, e.Screen("web", "main", nil)["b2"]["value"])
	if v.HTML != "<p>chart</p>" || len(v.Items) != 2 || v.Items[0]["title"] != "a" || v.Items[0]["_id"] == nil || v.Items[0]["_updated"] != nil ||
		len(v.Actions) != 4 || v.Actions[0]["command"] != "web.mark" || v.Actions[0]["class"] != "internal" || v.Actions[3]["class"] != "external" {
		t.Fatalf("a custom block gets its HTML, the items of its source and its buttons: %+v", v)
	}

	r0, err := http.Get(e.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	r0.Body.Close()
	if got := r0.Header.Get("Content-Security-Policy"); got != "frame-src 'self' "+e.Side+" blob:; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'self'" {
		t.Fatalf("the client's frames stay on its origins, so a block cannot move its frame to the network, and its pictures and sound stay on deck: %q", got)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := noFollow.Get(e.URL + "/custom")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || r.Header.Get("Location") != e.Side+"/sandbox.html" {
		t.Fatalf("the frame goes to the second origin: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	r, err = http.Get(e.Side + "/sandbox.html")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(r.Body)
	r.Body.Close()
	csp := r.Header.Get("Content-Security-Policy")
	for _, want := range []string{"connect-src 'none'", "default-src 'none'", "form-action 'none'", "frame-ancestors " + e.URL, "script-src 'self' " + e.Side + " 'unsafe-inline'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("the proxy page keeps the block off the network: %q lacks %q", csp, want)
		}
	}
	if !strings.Contains(string(page), `const host = "`+e.URL+`"`) {
		t.Fatalf("the proxy talks only to the client's origin: %s", page)
	}
	for _, path := range []string{"/api/commands", "/api/events", "/", "/mcp"} {
		r, err := http.Get(e.Side + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("the second origin has no API: %s gives %d", path, r.StatusCode)
		}
	}
	r, err = http.Get(e.Side + "/ext-apps.js")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("the second origin serves the MCP Apps library to blocks: %d", r.StatusCode)
	}
}

// TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord: an expression of a custom block's button reads the
// record of the page, whatever the block reads.
func TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	page := `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
	custom := func(field string) string {
		return strings.Replace(harness.WebNotes, page, page+`, {"type": "custom", "document": "custom/c.html", "source": {"from": "deck.asks_list"},
			"actions": [{"label": "Touch", "command": "touch", "params": {"note": "=`+field+`"}}]}`, 1)
	}
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("score")}, nil)
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("what")}, 422, `unknown field "what"`)
}

// TestCustomBlockGetsItsParamsResolved: params of a custom block are fixed text or expressions over the param of the
// screen and the record of the page, resolved by deck and given beside its items; an unknown field fails the save.
func TestCustomBlockGetsItsParamsResolved(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	chart := `"document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},`
	page := `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
	m := strings.NewReplacer(`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"kind": "string"}, "blocks": [`,
		chart, chart+` "params": {"mode": "voice", "speed": "=1.5", "kind": "=param.kind", "none": ""},`,
		page, page+`, {"type": "custom", "document": "custom/chart.html", "params": {"title": "=title + '!'", "double": "=score * 2"}}`).Replace(harness.WebNotes)
	e.Project("web", m)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	var put struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a", "score": 2}`}, &put)
	type custom struct {
		Params map[string]any   `json:"params"`
		Items  []map[string]any `json:"items"`
	}
	v := harness.BlockData[custom](t, e.Screen("web", "main", map[string]any{"params": `{"kind": "video"}`})["b2"]["value"])
	if len(v.Items) != 1 || len(v.Params) != 4 || v.Params["mode"] != "voice" || v.Params["speed"] != 1.5 || v.Params["kind"] != "video" || v.Params["none"] != nil {
		t.Fatalf("a custom block gets its params beside its items, fixed and resolved over the param of the screen: %+v", v)
	}
	v = harness.BlockData[custom](t, e.Screen("web", "note", map[string]any{"record": put.IDs[0]})["b1"]["value"])
	if len(v.Params) != 2 || v.Params["title"] != "a!" || v.Params["double"] != float64(4) {
		t.Fatalf("params of a custom block on a record page read the record: %+v", v.Params)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(m, `"=score * 2"`, `"=what"`, 1)},
		422, `params "double": unknown field "what"`)
}

// TestAHiddenButtonOfACustomBlockLeavesTheClassOfTheOthers: a button whose when does not hold is not there, and
// each of the others keeps the class of its own command, so an external call still waits for the owner.
func TestAHiddenButtonOfACustomBlockLeavesTheClassOfTheOthers(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	m := strings.Replace(harness.WebNotes, `"actions": [{"label": "Mark", "command": "mark"},`, `"actions": [{"label": "Mark", "command": "mark", "when": "false"},`, 1)
	if m == harness.WebNotes {
		t.Fatal("fixture lacks the Mark button")
	}
	e.Project("web", m)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	v := harness.BlockData[struct {
		Actions []map[string]any `json:"actions"`
	}](t, e.Screen("web", "main", nil)["b2"]["value"])
	if len(v.Actions) != 3 || v.Actions[2]["command"] != "web.publish" || v.Actions[2]["class"] != "external" || v.Actions[0]["class"] != "internal" {
		t.Fatalf("each button keeps the class of its command: %+v", v.Actions)
	}
}

// TestACustomBlockGetsOnlyTheFieldsItsSourceNames: source.fields cut each item of a custom block to them and its id,
// after where and sort read the whole item; another block is refused them.
func TestACustomBlockGetsOnlyTheFieldsItsSourceNames(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	chart := `"document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},`
	cut := `"document": "custom/chart.html", "source": {"collection": "notes", "where": "score > 1", "sort": "-score", "fields": ["title"]},`
	e.Project("web", strings.Replace(harness.WebNotes, chart, cut, 1))
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a", "score": 2}` + "\n" + `{"title": "b", "score": 3}` + "\n" + `{"title": "c", "score": 1}`}, nil)
	v := harness.BlockData[struct {
		Items []map[string]any `json:"items"`
	}](t, e.Screen("web", "main", nil)["b2"]["value"])
	if len(v.Items) != 2 || v.Items[0]["title"] != "b" || len(v.Items[0]) != 2 || v.Items[0]["_id"] == nil || v.Items[0]["score"] != nil {
		t.Fatalf("each item has only the fields named and its id, picked and sorted by the whole item: %+v", v.Items)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(harness.WebNotes, chart, `"document": "custom/chart.html", "source": {"collection": "notes", "fields": ["what"]},`, 1)}, 422, `unknown field "what"`)
	table := strings.Replace(harness.WebNotes, `{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "table", "source": {"collection": "notes", "fields": ["title"]}, "columns": [{"label": "Title", "bind": "title"}]}`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": table}, 422, "fields are for a custom block")
}

// TestACustomButtonNamesTheFieldsItsBlockFills: from_block names the input fields a custom block fills itself, a
// required one too; a record of them is marked for the host to take only an item's, and only on a custom block.
func TestACustomButtonNamesTheFieldsItsBlockFills(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	mark := `{"label": "Mark", "command": "mark"}`
	with := func(button string) string {
		return strings.Replace(harness.WebNotes, mark, `{"label": "Mark", "command": "mark", "from_block": ["text"]}, `+button, 1)
	}
	e.Project("web", with(`{"label": "Score", "command": "score", "from_block": ["title", "n"], "params": {"scan": "x"}}, {"label": "Touch", "command": "touch", "from_block": ["note"]}`))
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": with(`{"label": "Score", "command": "score"}`)}, 422, `needs param "title"`)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	v := harness.BlockData[struct {
		Actions []map[string]any `json:"actions"`
	}](t, e.Screen("web", "main", nil)["b2"]["value"])
	if fmt.Sprint(v.Actions[0]["from_block"]) != "[text]" || fmt.Sprint(v.Actions[1]["from_block"]) != "[title n]" || v.Actions[1]["from_items"] != nil ||
		fmt.Sprint(v.Actions[2]["from_items"]) != "[note]" || v.Actions[3]["from_block"] != nil {
		t.Fatalf("a button gives its block the fields it may fill: %+v", v.Actions)
	}
	for button, want := range map[string]string{
		`{"label": "Score", "command": "score", "from_block": ["title", "what"]}`:                   `no input field "what"`,
		`{"label": "Score", "command": "score", "from_block": ["title"], "params": {"title": "a"}}`: "is a param of the button already",
	} {
		e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": with(button)}, 422, want)
	}
	table := strings.Replace(harness.WebNotes, `"actions": [{"label": "Open", "screen": "note"}]`, `"actions": [{"label": "Mark", "command": "mark", "from_block": ["text"]}]`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": table}, 422, "from_block names the fields a custom block fills")
	deckRows := strings.Replace(with(`{"label": "Touch", "command": "touch", "from_block": ["note"]}`), `"source": {"collection": "notes", "sort": "title"},`, `"source": {"from": "deck.asks_list"},`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": deckRows}, 422, "the block holds no records of the module")
}

// TestATableAndACustomBlockHaveButtonsInTheirHead: a table and a custom block take a head as a list does, over the
// rows of their source; an empty table keeps its head, so its one button needs no actions block.
func TestATableAndACustomBlockHaveButtonsInTheirHead(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	m := strings.Replace(harness.WebNotes, `"document": "custom/chart.html",`, `"document": "custom/chart.html", "head": [{"label": "Mark all", "command": "mark"}],`, 1)
	m = strings.Replace(m, `"columns": [{"label": "Title", "bind": "title"}], "actions"`,
		`"columns": [{"label": "Title", "bind": "title"}], "head": [{"label": "='Mark (' + string(len(rows)) + ')'", "command": "mark"}], "actions"`, 1)
	e.Project("web", m)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	head := func(block string) []map[string]any {
		return harness.BlockData[[]map[string]any](t, e.Screen("web", "main", nil)[block]["head"])
	}
	if h := head("b3"); len(h) != 1 || h[0]["label"] != "Mark (0)" || h[0]["command"] != "web.mark" {
		t.Fatalf("an empty table keeps the button of its head: %+v", h)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "b"}` + "\n" + `{"title": "a"}`}, nil)
	if h := head("b3"); len(h) != 1 || h[0]["label"] != "Mark (2)" {
		t.Fatalf("the head of a table reads its rows: %+v", h)
	}
	if h := head("b2"); len(h) != 1 || h[0]["label"] != "Mark all" || h[0]["command"] != "web.mark" {
		t.Fatalf("a custom block has the buttons of its head: %+v", h)
	}
}

// TestACustomBlockReadsOnlyTheFilesOfItsModule: the host of a custom block reads a file of the module of its screen,
// stored or in its data folder, whole; a file of another module, a path out of the data folder or one too big is refused.
func TestACustomBlockReadsOnlyTheFilesOfItsModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("web", harness.WebNotes)
	e.Project("other", harness.PlainManifest)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "pics"), 0o700)
	png := "\x89PNG\r\n\x1a\nfake"
	_ = os.WriteFile(filepath.Join(data, "pics", "a b.png"), []byte(png), 0o600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(data), "secret.txt"), []byte("outside"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "big.mp4"), nil, 0o600)
	_ = os.Truncate(filepath.Join(data, "big.mp4"), 201<<20)
	own := e.Upload("web", "logo.svg", "image/svg+xml", "<svg/>")
	theirs := e.Upload("other", "theirs.txt", "text/plain", "theirs")

	block := harness.BlockData[map[string]any](t, e.Screen("web", "main", nil)["b2"]["value"])
	if block["project"] != "web" {
		t.Fatalf("a custom block knows the module it reads files of: %+v", block["project"])
	}
	type read struct {
		URI, Name, Type, Blob string
		Size                  int64
	}
	var got read
	e.Must("deck.file_read", map[string]any{"project": "web", "uri": "deck://data/pics/a%20b.png"}, &got)
	if b, _ := base64.StdEncoding.DecodeString(got.Blob); string(b) != png || got.Type != "image/png" || got.Name != "a b.png" || got.URI != "deck://data/pics/a%20b.png" {
		t.Fatalf("a file of the data folder is read whole with its type: %+v", got)
	}
	e.Must("deck.file_read", map[string]any{"project": "web", "uri": fmt.Sprintf("deck://file/%d", own)}, &got)
	if b, _ := base64.StdEncoding.DecodeString(got.Blob); string(b) != "<svg/>" || got.Type != "image/svg+xml" || got.Name != "logo.svg" {
		t.Fatalf("a stored file of the module is read whole with its type: %+v", got)
	}
	for _, uri := range []string{fmt.Sprintf("deck://file/%d", theirs), "deck://data/../secret.txt", "deck://data/%2e%2e/secret.txt",
		"deck://data/pics/../../secret.txt", "deck://data/", "https://example.com/a.png", "deck://file/x", "/api/files/1"} {
		if code, out := e.Call("deck.file_read", map[string]any{"project": "web", "uri": uri}); code == 200 || strings.Contains(string(out), "outside") || strings.Contains(string(out), "dGhlaXJz") {
			t.Fatalf("%s is not a file of the module: %d %s", uri, code, out)
		}
	}
	if code, out := e.Call("deck.file_read", map[string]any{"project": "web", "uri": "deck://data/big.mp4"}); code == 200 || !strings.Contains(string(out), "200 MB") {
		t.Fatalf("a file over 200 MB is refused with why: %d %s", code, out)
	}
	if code, _ := e.Call("deck.file_read", map[string]any{"project": "other", "uri": fmt.Sprintf("deck://file/%d", theirs)}); code != 200 {
		t.Fatalf("the other module reads its own file: %d", code)
	}
}

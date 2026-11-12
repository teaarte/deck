package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/internal/words"
	"deck2/tests/harness"
)

// rightRow is a row of deck.rights_list.
type rightRow struct {
	Right, Who, Subject, Says, Why, Answer string
	Declared                               bool
	AtUse                                  bool `json:"at_use"`
}

func rightsOf(e *harness.Env, project string) []rightRow {
	e.T.Helper()
	var rows []rightRow
	e.Must("deck.rights_list", map[string]any{"project": project}, &rows)
	return rows
}

func answerOf(rows []rightRow, right, subject string) (string, bool) {
	i := slices.IndexFunc(rows, func(r rightRow) bool { return r.Right == right && r.Subject == subject })
	if i < 0 {
		return "", false
	}
	return rows[i].Answer, true
}

// TestASendingRightAsksOnceAndAlwaysHoldsToWhatItWasGivenTo: sending asks on its first call; always lets the same
// call through, a changed tool or a revoke asks again, and a no refuses without asking.
func TestASendingRightAsksOnceAndAlwaysHoldsToWhatItWasGivenTo(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	rows := rightsOf(e, "notes")
	if a, ok := answerOf(rows, "send", "post"); !ok || a != "" || !strings.Contains(rows[0].Says, "it acts in your name: post") || rows[0].Why != "posts notes to the service" {
		t.Fatalf("an enabled module declares sending, which waits for its first use: %+v", rows)
	}
	post := func() (int, int64) {
		code, out := e.As(tok).Call("notes.post", map[string]any{"id": "a", "text": "hi"})
		var w struct{ Waiting int64 }
		_ = json.Unmarshal(out, &w)
		return code, w.Waiting
	}
	code, ask := post()
	if code != 200 || ask == 0 || len(svc.Requests()) != 0 {
		t.Fatalf("the first call asks the owner and sends nothing: %d %d, %d requests", code, ask, len(svc.Requests()))
	}
	e.Must("deck.ask_answer", map[string]any{"ask": ask, "answer": "allow_always"}, nil)
	if n := len(svc.Requests()); n != 1 {
		t.Fatalf("allowing makes the call: %d requests", n)
	}
	if a, _ := answerOf(rightsOf(e, "notes"), "send", "post"); a != "always" {
		t.Fatalf("always is kept as the owner's answer: %q", a)
	}
	if _, ask := post(); ask != 0 || len(svc.Requests()) != 2 {
		t.Fatalf("the same call goes without asking: ask %d, %d requests", ask, len(svc.Requests()))
	}
	changed := strings.Replace(harness.PostManifest(svc.URL, ""), `/items/{id}`, `/other/{id}`, 1)
	if code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": changed}); code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("a change of the tool saves at once: %d %s", code, out)
	}
	if _, ask := post(); ask == 0 || len(svc.Requests()) != 2 {
		t.Fatalf("a changed tool asks again, as always held to what it was given to: ask %d, %d requests", ask, len(svc.Requests()))
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "always"}, nil)
	if _, ask := post(); ask != 0 || len(svc.Requests()) != 3 {
		t.Fatalf("always given in the settings holds to the tool as it is now: ask %d, %d requests", ask, len(svc.Requests()))
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "revoke"}, nil)
	if _, ask := post(); ask == 0 {
		t.Fatalf("a revoked right asks again")
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "no"}, nil)
	if code, out := e.As(tok).Call("notes.post", map[string]any{"id": "b", "text": "hi"}); code == 200 || !strings.Contains(string(out), "the owner said no") {
		t.Fatalf("a no refuses without asking: %d %s", code, out)
	}
}

// TestWhatAModuleDoesNotDeclareItCannotAsk: a manifest that needs a right it does not declare is refused with what to
// declare, and the owner cannot grant what it does not declare.
func TestWhatAModuleDoesNotDeclareItCannotAsk(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	post := strings.Replace(harness.PostManifest("https://api.example", ""), `"rights": [{"right": "send", "what": ["post"], "why": "posts notes to the service"}]`, `"rights": []`, 1)
	for _, c := range []struct{ name, manifest, says string }{
		{"tool", post, `http tool "post": it needs the right send on post; declare it in rights with why`},
		{"model", harness.Manifest(`{"commands": [{"name": "say", "title": "Say", "description": "d", "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]},
      "executor": {"type": "model", "kind": "tts"}}]}`), `its model goes through @tts; declare it under money in rights with why`},
		{"catalog", harness.Manifest(`{"rights": [{"right": "microphone", "what": ["x"], "why": "listens"}]}`), `does not match the format`},
		{"login", harness.Manifest(`{"rights": [{"right": "login", "what": ["claude-code"], "why": "w"}, {"right": "network", "who": "agent", "what": ["x.example"], "why": "w"}]}`),
			"an agent on a subscription login never gets the network in its shell"},
		{"others", harness.Manifest(`{"rights": [{"right": "data", "what": ["other"], "why": "w"}, {"right": "network", "who": "agent", "what": ["x.example"], "why": "w"}]}`),
			"the agents of a module that reads the data of others never get the network in their shells"},
		{"suffix", harness.Manifest(`{"rights": [{"right": "network", "what": ["co.uk"], "why": "w"}]}`), "a public suffix would open every site under it"},
		{"undo", strings.Replace(harness.PlainManifest, `"executor": {`, `"undo": "gone", "executor": {`, 1), `undo: "gone" is neither another command nor a tool`},
	} {
		code, out := e.Call("deck.project_create", map[string]any{"name": "x-" + c.name, "manifest": c.manifest})
		if code != 422 || !strings.Contains(string(out), c.says) {
			t.Errorf("%s: a save is refused with what to declare: %d %s", c.name, code, out)
		}
	}
	e.Project("plain", harness.PlainManifest)
	if code, out := e.Call("deck.right_set", map[string]any{"project": "plain", "right": "network", "subject": "example.com", "answer": "always"}); code != 422 || !strings.Contains(string(out), "does not declare") {
		t.Fatalf("the owner grants only what a module declares: %d %s", code, out)
	}
}

// TestEnablingADraftShowsAndGrantsWhatItDeclares: the card of a draft says in deck's words what each declared right
// lets it do and why in its own; enabling grants them but sending, which asks the first time.
func TestEnablingADraftShowsAndGrantsWhatItDeclares(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "draft", "manifest": harness.DraftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	rows := rightsOf(e, "draft")
	for _, r := range rows {
		if r.Answer != "" || !r.Declared {
			t.Fatalf("a draft holds nothing it declares: %+v", rows)
		}
	}
	var card []struct {
		Rights   string
		Revision int64
	}
	e.Must("deck.project_draft_list", map[string]any{"project": "draft"}, &card)
	if len(card) != 1 || !strings.Contains(card[0].Rights, "its programs reach example.com and may send its data there — reads its pages") ||
		!strings.Contains(card[0].Rights, "it acts in your name: send — sends what it made — asks you the first time") {
		t.Fatalf("the card says each right in deck's words and why in the module's: %+v", card)
	}
	var ru []struct{ Rights string }
	e.In("ru").Must("deck.project_draft_list", map[string]any{"project": "draft"}, &ru)
	if says := fmt.Sprintf(words.In("ru")["its programs reach %s and may send its data there"], "example.com"); len(ru) != 1 || says == "" || !strings.Contains(ru[0].Rights, says) {
		t.Fatalf("the card speaks the owner's language: %+v", ru)
	}
	e.Must("deck.project_enable", map[string]any{"project": "draft", "revision": card[0].Revision}, nil)
	rows = rightsOf(e, "draft")
	if a, _ := answerOf(rows, "network", "example.com"); a != "always" {
		t.Fatalf("enabling grants the network it declares: %+v", rows)
	}
	if a, _ := answerOf(rows, "send", "send"); a != "" {
		t.Fatalf("sending asks the first time even after enabling: %+v", rows)
	}
}

// TestADeclarationAddedLaterWaitsForTheOwnersGrant: an agent's save that declares a new right saves at once and grants
// nothing: its agents read the data of another module only once the owner grants it.
func TestADeclarationAddedLaterWaitsForTheOwnersGrant(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var reads []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad := c.Tool(ctx, "records_list", map[string]any{"project": "source", "collection": "items"})
		mu.Lock()
		reads = append(reads, fmt.Sprint(bad, " ", out))
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))
	e.Project("source", harness.FilesManifest())
	e.Must("deck.record_upsert", map[string]any{"project": "source", "collection": "items", "records": harness.Jsonl(map[string]any{"name": "one"})}, nil)
	e.Project("reader", harness.PlainManifest)
	_, tok := e.Token("terminal", "deck.*")
	wider := harness.ManifestWith(harness.PlainManifest, `"rights": [{"right": "data", "what": ["source.items"], "why": "lists the items of source"}]`)
	var saved struct {
		Revision  int64
		Ungranted []string
	}
	code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "reader", "manifest": wider})
	if code != 200 || json.Unmarshal(out, &saved) != nil || saved.Revision == 0 || !slices.Equal(saved.Ungranted, []string{"it reads your data: source.items"}) {
		t.Fatalf("a new declaration saves at once and says it is not granted: %d %s", code, out)
	}
	e.WaitState(e.Start("reader", "work", nil), "done")
	e.Grant("reader", "data", "source.items")
	e.WaitState(e.Start("reader", "work", nil), "done")
	mu.Lock()
	defer mu.Unlock()
	if len(reads) != 2 || !strings.HasPrefix(reads[0], "true ") || !strings.Contains(reads[0], "the owner has not granted it") ||
		!strings.HasPrefix(reads[1], "false ") || !strings.Contains(reads[1], "one") {
		t.Fatalf("the agent reads another module only once the owner grants it: %q", reads)
	}
}

// TestTheMasterHoldsWhatDeckShipsNotWhatItWrites: the master holds the rights deck's own manifest of it declares;
// one it declares itself in an edit stays ungranted.
func TestTheMasterHoldsWhatDeckShipsNotWhatItWrites(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	rows := rightsOf(e, "master")
	for _, want := range [][2]string{{"login", "claude-code"}, {"login", "codex"}, {"data", "*"}, {"modules", "*"}, {"network", "*"}} {
		if a, _ := answerOf(rows, want[0], want[1]); a != "always" {
			t.Fatalf("the master holds %s on %s as deck ships it: %+v", want[0], want[1], rows)
		}
	}
	var m struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": "manifest.json"}, &m)
	edited := strings.Replace(m.Text, `"rights": [`, `"rights": [{"right": "files", "what": ["gpu"], "why": "renders"}, `, 1)
	e.Must("deck.manifest_save", map[string]any{"project": "master", "manifest": edited}, nil)
	e = e.Restart()
	if a, ok := answerOf(rightsOf(e, "master"), "files", "gpu"); !ok || a != "" {
		t.Fatalf("a right the master declares itself is not granted: %q %v", a, ok)
	}
}

// TestTheOwnerAnswersARightOnTheScreensOfTheModule: the settings of a module and the card of a draft list its rights
// in deck's words with buttons; a press answers always or revokes.
func TestTheOwnerAnswersARightOnTheScreensOfTheModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PostManifest("https://api.example", ""))
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("notes", "settings", "Rights", nil)["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != "it acts in your name: post" || rows[0].Cells[1] != "asks first" {
		t.Fatalf("the settings of a module list its rights: %+v", rows)
	}
	if code, out := e.Press(e.RowAction("notes", "settings", "deck.right_set", map[string]any{"answer": "always"})); code != 200 {
		t.Fatalf("allow always on the screen: %d %s", code, out)
	}
	if a, _ := answerOf(rightsOf(e, "notes"), "send", "post"); a != "always" {
		t.Fatalf("the press is the owner's answer: %q", a)
	}
	if code, out := e.Press(e.RowAction("notes", "settings", "deck.right_set", map[string]any{"answer": "revoke"})); code != 200 {
		t.Fatalf("revoke on the screen: %d %s", code, out)
	}
	if a, _ := answerOf(rightsOf(e, "notes"), "send", "post"); a != "" {
		t.Fatalf("a revoked right asks again: %q", a)
	}
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "draft", "manifest": harness.DraftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	params := map[string]any{"params": `{"project": "draft", "revision": "1"}`}
	press := e.Button("", "draft", params, "deck.right_set", map[string]any{"subject": "example.com", "answer": "always"})
	if code, out := e.Press(press); code != 200 {
		t.Fatalf("a right of a draft is granted on its card before enabling: %d %s", code, out)
	}
	if a, _ := answerOf(rightsOf(e, "draft"), "network", "example.com"); a != "always" {
		t.Fatalf("the card's press grants it: %q", a)
	}
}

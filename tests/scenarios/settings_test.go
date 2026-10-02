package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/store/db"
)

// TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice: the look is the client's, so the block
// carries no data, and a project screen cannot hold it.
func TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice(t *testing.T) {
	e := newEnv(t, nil)
	var menu []struct {
		Project string
		Screens []struct{ Name, Title string }
	}
	e.must("deck.menu", nil, &menu)
	var names []string
	for _, s := range menu[0].Screens {
		names = append(names, s.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "cockpit schedule settings") {
		t.Fatalf("the deck menu is the cockpit, the schedule and settings: %v", names)
	}
	check := newCatalogCheck(t)
	msgs := e.screenMessages("", "settings", nil)
	check.messages(t, msgs)
	var look string
	for id, comp := range check.comps {
		if comp == "appearance" {
			look = id
		}
	}
	if look == "" {
		t.Fatalf("settings has the appearance block: %v", check.comps)
	}
	if got := string(e.screenData("", "settings", nil)[look]["state"]); got != `"ready"` {
		t.Fatalf("the appearance block is ready without a source: %s", got)
	}
	if n := len(e.screenMessages("", "settings", map[string]any{"topic": "processes"})); n != 0 {
		t.Fatalf("no event reads the look again: %d messages", n)
	}

	e.createProject("web", boardWebManifest)
	bad := strings.Replace(boardWebManifest, `{"type": "board", "title": "Work"}`, `{"type": "appearance"}`, 1)
	code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a project screen holds no appearance block: %d %s", code, out)
	}
}

// settingsBlock is the data of the block of a deck screen with this title, found by its component.
func (e *Env) settingsBlock(title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	msgs := e.screenMessages("", "settings", nil)
	id := ""
	for _, m := range msgs {
		if raw, ok := m["updateComponents"]; ok {
			var uc struct{ Components []map[string]any }
			_ = json.Unmarshal(raw, &uc)
			for _, c := range uc.Components {
				if c["title"] == title && c["component"] != "tabs" {
					id = c["id"].(string)
				}
			}
		}
	}
	if id == "" {
		e.T.Fatalf("settings has no block %q", title)
	}
	if extra["topic"] == nil {
		return e.screenData("", "settings", extra)[id]
	}
	for _, m := range e.screenMessages("", "settings", extra) {
		var dm struct {
			Path  string
			Value map[string]json.RawMessage
		}
		if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path == "/"+id {
			return dm.Value
		}
	}
	e.T.Fatalf("topic %v does not read block %q", extra["topic"], title)
	return nil
}

func findItem(t *testing.T, items []listItem, prefix string) listItem {
	t.Helper()
	for _, it := range items {
		if strings.HasPrefix(it.Title, prefix) {
			return it
		}
	}
	t.Fatalf("no item %q in %+v", prefix, items)
	return listItem{}
}

func verb(t *testing.T, acts []map[string]any, v string) map[string]any {
	t.Helper()
	for _, a := range acts {
		if a["verb"] == v {
			return a
		}
	}
	t.Fatalf("no %s button in %+v", v, acts)
	return nil
}

func hasVerb(acts []map[string]any, v string) bool {
	return slices.ContainsFunc(acts, func(a map[string]any) bool { return a["verb"] == v })
}

// TestSettingsProjectsEnableADraftAtTheRevisionItShowsAndSwitchAutonomy: a draft shows the rights enabling
// grants; the enable button carries the revision the row shows, so a change after it is refused.
func TestSettingsProjectsEnableADraftAtTheRevisionItShowsAndSwitchAutonomy(t *testing.T) {
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": draftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	items := func() []listItem { return blockData[[]listItem](t, e.settingsBlock("Projects", nil)["items"]) }
	press := func(a map[string]any) { t.Helper(); e.must(a["command"].(string), a["params"], nil) }
	notes := findItem(t, items(), "notes")
	if notes.Title != "notes · draft" || !strings.Contains(notes.Meta, "autonomy: suggest") ||
		!strings.Contains(notes.Meta, "enabling grants: "+strings.Join(e.project("notes").Rights, "; ")) {
		t.Fatalf("a draft shows its autonomy and the rights enabling grants: %+v", notes)
	}
	stale := verb(t, notes.Actions, "enable")
	if stale["params"].(map[string]any)["revision"] != float64(e.manifestRevision("notes")) {
		t.Fatalf("enable carries the revision the row shows: %+v", stale)
	}

	press(verb(t, notes.Actions, "autonomy_auto"))
	notes = findItem(t, items(), "notes")
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &m)
	_, granted, _ := strings.Cut(notes.Meta, "enabling grants: ")
	if !strings.HasPrefix(notes.Meta, "autonomy: auto") || !strings.Contains(granted, "autonomy: auto") ||
		!strings.Contains(m.Manifest, `"autonomy": "auto"`) || !strings.Contains(m.Manifest, `"prompt": "Do the work."`) {
		t.Fatalf("autonomy is a new revision of the manifest, the rest kept; enabling now grants it: %+v\n%s", notes, m.Manifest)
	}
	e.refused(stale["command"].(string), stale["params"], 409, "look at its rights again")
	press(verb(t, notes.Actions, "enable"))
	notes = findItem(t, items(), "notes")
	if notes.Title != "notes · enabled" || hasVerb(notes.Actions, "enable") || strings.Contains(notes.Meta, "grants") {
		t.Fatalf("an enabled project has no enable button and asks for nothing: %+v", notes)
	}
	press(verb(t, notes.Actions, "autonomy_suggest"))
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &m)
	if strings.Contains(m.Manifest, "autonomy") || !strings.Contains(findItem(t, items(), "notes").Meta, "autonomy: suggest") {
		t.Fatalf("suggest is the default and leaves the manifest without autonomy: %s", m.Manifest)
	}
	press(verb(t, findItem(t, items(), "notes").Actions, "pause"))
	if notes = findItem(t, items(), "notes"); notes.Title != "notes · enabled, paused" || !hasVerb(notes.Actions, "resume") {
		t.Fatalf("a paused project says so and resumes: %+v", notes)
	}
	if code, _ := e.callWith(tok, "deck.project_autonomy", map[string]any{"project": "notes", "autonomy": "auto"}); code != 403 {
		t.Fatalf("only the owner switches autonomy: %d", code)
	}
	if f := e.settingsBlock("Trial run", nil); !strings.Contains(string(f["value"]), `"deck.command_try"`) {
		t.Fatalf("a trial run is a form on the projects tab: %s", f["value"])
	}
}

// TestSettingsSecretsAndTokensShowOnlyNames: a secret's value is a hidden field and never comes back; a new
// token is shown once, by its form, and the list has only its name and commands.
func TestSettingsSecretsAndTokensShowOnlyNames(t *testing.T) {
	e := newEnv(t, nil)
	events := e.sse(t.Context())
	form := blockData[formV](t, e.settingsBlock("Set or replace a secret", nil)["value"])
	if form.Commands[0].Input.Properties["value"]["writeOnly"] != true {
		t.Fatalf("the value of a secret is a field that hides what is typed: %+v", form.Commands[0].Input.Properties)
	}
	e.must("deck.secret_set", map[string]any{"name": "mail-key", "value": "s3cret-value"}, nil)
	waitTopic(t, events, "secrets")
	secrets := e.settingsBlock("Secrets", map[string]any{"topic": "secrets"})
	if got := blockData[[]listItem](t, secrets["items"]); len(got) != 1 || got[0].Title != "mail-key" {
		t.Fatalf("the secrets tab lists names and follows the secrets topic: %s", secrets["items"])
	}

	var made struct {
		Token      string
		ClaudeCode string `json:"claude_code"`
	}
	e.must("deck.token_create", map[string]any{"name": "shortcuts", "commands": []string{"deck.notify", "web.*"}}, &made)
	waitTopic(t, events, "tokens")
	all, _ := json.Marshal(e.screenData("", "settings", nil))
	if strings.Contains(string(all), "s3cret-value") || strings.Contains(string(all), made.Token) || !strings.Contains(made.ClaudeCode, made.Token) {
		t.Fatalf("no secret value or token is on the screen after it was made: %s", all)
	}
	rows := blockData[[]screenRow](t, e.settingsBlock("Automation tokens", map[string]any{"topic": "tokens"})["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != "shortcuts" || rows[0].Cells[1] != "deck.notify, web.*" || rows[0].Cells[3] != "live" {
		t.Fatalf("a token shows its name, commands and state: %+v", rows)
	}
	revoke := verb(t, rows[0].Actions, "revoke")
	if revoke["confirm"] != true {
		t.Fatalf("revoking asks the owner first: %+v", revoke)
	}
	e.must(revoke["command"].(string), revoke["params"], nil)
	waitTopic(t, events, "tokens")
	rows = blockData[[]screenRow](t, e.settingsBlock("Automation tokens", nil)["rows"])
	if rows[0].Cells[3] != "revoked" || hasVerb(rows[0].Actions, "revoke") {
		t.Fatalf("a revoked token stays in the list without the button: %+v", rows)
	}
	if code, _ := e.callWith(made.Token, "deck.notify", map[string]any{"text": "hi"}); code != 401 {
		t.Fatalf("the revoked token no longer calls: %d", code)
	}
	var journal []map[string]any
	e.must("deck.calls_list", nil, &journal)
	if len(journal) != 1 || journal[0]["tool"] != "deck.token_revoke" || journal[0]["class"] != "external" || journal[0]["actor"] != "owner" {
		t.Fatalf("revoking a token is an external call in the journal: %+v", journal)
	}
	var listed []struct{ Name, Class string }
	_, raw := e.do("GET", "/api/commands", nil)
	_ = json.Unmarshal(raw, &listed)
	for _, c := range listed {
		if c.Name == "deck.token_revoke" && c.Class != "external" {
			t.Fatalf("revoking a token is external: %q", c.Class)
		}
	}
	_, other := e.token("terminal", "deck.*")
	if code, _ := e.callWith(other, "deck.calls_list", nil); code != 403 {
		t.Fatalf("only the owner reads the journal: %d", code)
	}
}

// TestSettingsProvidersAddAndRemoveOnTheScreen: the providers tab adds a provider with a form and removes
// an added one after the owner says yes; built-in providers have no remove.
func TestSettingsProvidersAddAndRemoveOnTheScreen(t *testing.T) {
	e := newEnv(t, nil)
	events := e.sse(t.Context())
	if f := e.settingsBlock("Add a provider", nil); !strings.Contains(string(f["value"]), `"deck.provider_add"`) {
		t.Fatalf("the providers tab adds a provider with a form: %s", f["value"])
	}
	e.must("deck.provider_add", map[string]any{"name": "ollama", "url": "http://127.0.0.1:1/v1", "model": "small", "no_cloud": true}, nil)
	waitTopic(t, events, "health")
	rows := blockData[[]screenRow](t, e.settingsBlock("Providers", map[string]any{"topic": "health"})["rows"])
	var added screenRow
	for _, r := range rows {
		if r.Cells[0] == "ollama" {
			added = r
		} else if hasVerb(r.Actions, "remove") {
			t.Fatalf("a built-in provider cannot be removed: %+v", r)
		}
	}
	if hasVerb(added.Actions, "check") {
		t.Fatalf("a provider without a self-check has no check button: %+v", added.Actions)
	}
	remove := verb(t, added.Actions, "remove")
	if remove["confirm"] != true || added.Cells[2] != "small" {
		t.Fatalf("an added provider shows its model and is removed after a yes: %+v", added)
	}
	e.must(remove["command"].(string), remove["params"], nil)
	waitTopic(t, events, "health")
	for _, r := range blockData[[]screenRow](t, e.settingsBlock("Providers", nil)["rows"]) {
		if r.Cells[0] == "ollama" {
			t.Fatalf("the removed provider is gone: %+v", r)
		}
	}
}

// TestSettingsHealthShowsWhatIsRedFirstWithWhy: health is a table of subjects with their state and error,
// the worst first, narrowed by state.
func TestSettingsHealthShowsWhatIsRedFirstWithWhy(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	ctx := t.Context()
	for _, h := range []db.SetHealthParams{{Subject: "mcp:web.search", Status: "ok"}, {Subject: "tool:web.fetch", Status: "error", Error: "the server exited: 1"},
		{Subject: "provider:elsewhere", Status: "error", Error: "a provider of another workspace"}} {
		if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.SetHealth(ctx, h) }); err != nil {
			t.Fatal(err)
		}
	}
	block := e.settingsBlock("Health", nil)
	rows := blockData[[]screenRow](t, block["rows"])
	if len(rows) < 2 || rows[0].Cells[1] != "error" || rows[0].Cells[2] != "the server exited: 1" {
		t.Fatalf("what is red comes first with why: %+v", rows)
	}
	for _, r := range rows {
		if r.Cells[0] == "provider:elsewhere" {
			t.Fatalf("health shows only the providers of the workspace: %+v", r)
		}
	}
	if !strings.Contains(string(block["filters"]), `"error"`) {
		t.Fatalf("health narrows by state: %s", block["filters"])
	}
	id := ""
	for k, v := range e.screenData("", "settings", nil) {
		if string(v["rows"]) == string(block["rows"]) {
			id = k
		}
	}
	picked := e.screenData("", "settings", map[string]any{"filter": `{"` + id + `": {"status": "ok"}}`})[id]
	for _, r := range blockData[[]screenRow](t, picked["rows"]) {
		if r.Cells[1] != "ok" {
			t.Fatalf("the filter keeps one state: %+v", r)
		}
	}
}

// TestTheDiskHealthReachesTheOpenScreens: a new disk reading tells the clients, so the Health tab is current.
func TestTheDiskHealthReachesTheOpenScreens(t *testing.T) {
	e := newEnv(t, nil)
	events := e.sse(t.Context())
	e.App.Sweep(context.Background(), time.Now().Add(time.Hour))
	waitTopic(t, events, "health")
}

package scenarios

import (
	"context"
	"deck2/internal/health"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice: the look is the client's, so the block
// carries no data, and a project screen cannot hold it.
func TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var menu []struct {
		Project string
		Screens []struct{ Name, Title string }
	}
	e.menuOf(&menu)
	var names []string
	for _, s := range menu[0].Screens {
		names = append(names, s.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "schedule spend files look settings") {
		t.Fatalf("the deck menu is the schedule, spend, files, look and settings; the cockpit is the link at its head: %v", names)
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
	if drawn, _ := json.Marshal(e.screenMessages("", "settings", map[string]any{"topic": "processes"})); strings.Contains(string(drawn), `"/`+look+`"`) {
		t.Fatalf("no event reads the look again: %s", drawn)
	}

	e.createProject("web", webNotes)
	bad := strings.Replace(webNotes, `{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "appearance"}`, 1)
	code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a project screen holds no appearance block: %d %s", code, out)
	}
}

// settingsBlock is the data of the block of deck's settings with this title, found by its component.
func (e *Env) settingsBlock(title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	return e.screenBlock("", "settings", title, extra)
}

// screenBlock is the data of the block of a screen with this title, found by its component in the tab it is in.
func (e *Env) screenBlock(project, screen, title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	id, shown := e.findBlock(project, screen, title)
	if id == "" {
		e.T.Fatalf("%s has no block %q", screen, title)
	}
	extra = merge(extra, shown)
	if extra["topic"] == nil {
		return e.screenData(project, screen, extra)[id]
	}
	for _, m := range e.screenMessages(project, screen, extra) {
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

// findBlock is the id of the block of a screen with this title and the tabs and dialog that show it: a block shown by a when in
// a tab is drawn only with its tab shown.
func (e *Env) findBlock(project, screen, title string) (string, map[string]any) {
	e.T.Helper()
	comps := func(extra map[string]any) []map[string]any {
		var out []map[string]any
		for _, m := range e.screenMessages(project, screen, extra) {
			var uc struct{ Components []map[string]any }
			if json.Unmarshal(m["updateComponents"], &uc) == nil {
				out = append(out, uc.Components...)
			}
		}
		return out
	}
	all := comps(nil)
	shown := func(drawn []map[string]any, id string) map[string]any {
		out := map[string]any{}
		n := 0
		for _, c := range drawn {
			p, _ := c["id"].(string)
			if name, _ := c["name"].(string); name != "" {
				p = strings.TrimSuffix(p, "-in")
			}
			if c["component"] != "screen" || id != p && !strings.HasPrefix(id, p+"-") {
				continue
			}
			if c["key"] != nil {
				out[[]string{"tab", "sub"}[min(n, 1)]] = c["key"]
				n++
			} else if name, _ := c["name"].(string); name != "" {
				out["dialog"] = name
			}
		}
		return out
	}
	find := func(cs []map[string]any) string {
		for _, c := range cs {
			if c["title"] == title {
				return c["id"].(string)
			}
		}
		for _, c := range cs {
			if c["component"] == "Text" && c["text"] == title {
				return c["id"].(string)
			}
		}
		return ""
	}
	if id := find(all); id != "" {
		return id, shown(all, id)
	}
	for _, c := range all {
		if p, _ := c["id"].(string); c["component"] == "screen" && c["key"] != nil {
			drawn := comps(shown(all, p))
			if id := find(drawn); id != "" {
				return id, shown(drawn, id)
			}
		}
	}
	return "", nil
}

// formID is the block id of the deck settings form of a command.
func formID(t *testing.T, e *Env, command string) string {
	t.Helper()
	for _, extra := range append([]map[string]any{nil}, e.dialogsOf("", "settings")...) {
		for id, d := range e.screenData("", "settings", extra) {
			if strings.Contains(string(d["value"]), `"name":"`+command+`"`) && strings.Contains(string(d["value"]), `"commands":[{`) {
				return id
			}
		}
	}
	t.Fatalf("no form of %s", command)
	return ""
}

// screenForm is the data of the form of a screen that calls this command, in a dialog or not.
func (e *Env) screenForm(project, screen, command string) map[string]json.RawMessage {
	e.T.Helper()
	for _, extra := range append([]map[string]any{nil}, e.dialogsOf(project, screen)...) {
		for _, d := range e.screenData(project, screen, extra) {
			if strings.Contains(string(d["value"]), `"commands":[{`) && strings.Contains(string(d["value"]), `"name":"`+command+`"`) {
				return d
			}
		}
	}
	e.T.Fatalf("%s has no form of %s", screen, command)
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

// TestSettingsProjectsEnableADraftAtTheRevisionItShows: a draft shows the rights enabling grants; the enable
// button carries the revision the row shows, so a change after it is refused.
func TestSettingsProjectsEnableADraftAtTheRevisionItShows(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": draftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	items := func() []listItem { return blockData[[]listItem](t, e.settingsBlock("Modules", nil)["items"]) }
	press := func(a map[string]any) { t.Helper(); e.must(a["command"].(string), a["params"], nil) }
	notes := findItem(t, items(), "notes")
	if notes.Title != "notes" || !strings.HasPrefix(notes.Meta, "draft") || strings.Contains(notes.Meta, "autonomy") ||
		!strings.Contains(notes.Meta, "enabling grants: "+strings.Join(e.project("notes").Rights, "; ")) {
		t.Fatalf("a draft shows the rights enabling grants: %+v", notes)
	}
	enable := func(review map[string]any) map[string]any {
		t.Helper()
		p := review["params"].(map[string]any)
		params, _ := json.Marshal(map[string]string{"project": fmt.Sprint(p["project"]), "revision": fmt.Sprint(p["revision"])})
		return e.button("", review["screen"].(string), map[string]any{"params": string(params)}, "deck.project_enable", nil)
	}
	stale := enable(verb(t, notes.Actions, "review"))
	if stale["params"].(map[string]any)["revision"] != float64(e.manifestRevision("notes")) {
		t.Fatalf("enable on the page of what a draft may do carries the revision the row shows: %+v", stale)
	}

	e.must("deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"title": "Work"`}, "new": []string{`"title": "Work again"`}}, nil)
	notes = findItem(t, items(), "notes")
	e.refused(stale["command"].(string), stale["params"], 409, "look at its rights again")
	press(enable(verb(t, notes.Actions, "review")))
	notes = findItem(t, items(), "notes")
	if notes.Meta != "" || hasVerb(notes.Actions, "review") {
		t.Fatalf("an enabled project has no enable button and asks for nothing: %+v", notes)
	}
	press(verb(t, findItem(t, items(), "notes").Actions, "pause"))
	if notes = findItem(t, items(), "notes"); !strings.HasPrefix(notes.Meta, "enabled, paused") || !hasVerb(notes.Actions, "resume") {
		t.Fatalf("a paused project says so and resumes: %+v", notes)
	}
	if f := e.screenForm("notes", "settings", "deck.command_try"); !strings.Contains(string(f["value"]), `"deck.command_try"`) {
		t.Fatalf("trying a command is a form of the settings of the project: %s", f["value"])
	}
}

// TestSettingsSecretsAndTokensShowOnlyNames: a secret's value is a hidden field and never comes back; a new
// token is shown once, by its form, and the list has only its name and commands.
func TestSettingsSecretsAndTokensShowOnlyNames(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	events := e.sse(t.Context())
	form := blockData[formV](t, e.screenForm("", "settings", "deck.secret_set")["value"])
	if form.Commands[0].Input.Properties["value"]["writeOnly"] != true {
		t.Fatalf("the value of a secret is a field that hides what is typed: %+v", form.Commands[0].Input.Properties)
	}
	e.must("deck.secret_set", map[string]any{"name": "mail-key", "value": "s3cret-value"}, nil)
	waitTopic(t, events, "secrets")
	secrets := e.settingsBlock("Secrets", map[string]any{"topic": "secrets"})
	got := blockData[[]screenRow](t, secrets["rows"])
	if len(got) != 1 || got[0].Cells[0] != "mail-key" || !hasVerb(got[0].Actions, "replace") || verb(t, got[0].Actions, "delete")["confirm"] != true {
		t.Fatalf("the secrets tab lists names with replace and delete, and follows the secrets topic: %s", secrets["rows"])
	}
	del := verb(t, got[0].Actions, "delete")
	e.must(del["command"].(string), del["params"], nil)
	waitTopic(t, events, "secrets")
	if left := blockData[[]screenRow](t, e.settingsBlock("Secrets", nil)["rows"]); len(left) != 0 {
		t.Fatalf("a deleted secret is gone: %+v", left)
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
	if len(rows) != 1 || rows[0].Cells[0] != "shortcuts" || rows[0].Cells[2] != "deck.notify, web.*" || rows[0].Cells[1] != "live" {
		t.Fatalf("a token shows its name, commands and state: %+v", rows)
	}
	revoke := verb(t, rows[0].Actions, "revoke")
	if revoke["confirm"] != true {
		t.Fatalf("revoking asks the owner first: %+v", revoke)
	}
	e.must(revoke["command"].(string), revoke["params"], nil)
	waitTopic(t, events, "tokens")
	rows = blockData[[]screenRow](t, e.settingsBlock("Automation tokens", nil)["rows"])
	if rows[0].Cells[1] != "revoked" || hasVerb(rows[0].Actions, "revoke") {
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
	var revoked commandView
	e.must("deck.command_describe", map[string]any{"command": "deck.token_revoke"}, &revoked)
	if revoked.Class != "external" {
		t.Fatalf("revoking a token is external: %q", revoked.Class)
	}
	_, other := e.token("terminal", "deck.*")
	if code, _ := e.callWith(other, "deck.calls_list", nil); code != 403 {
		t.Fatalf("only the owner reads the journal: %d", code)
	}
}

// TestSettingsModelsAddAndRemoveOnTheScreen: the models tab adds a model with a form and removes an added one after
// the owner says yes; built-in providers have no remove.
func TestSettingsModelsAddAndRemoveOnTheScreen(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	events := e.sse(t.Context())
	if f := e.screenForm("", "settings", "deck.model_set"); !strings.Contains(string(f["value"]), `"deck.model_set"`) {
		t.Fatalf("the models tab adds a model with a form: %s", f["value"])
	}
	e.must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": "http://127.0.0.1:1/v1", "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "ollama", "model": "small", "name": "ollama-small", "agent": "goose"}, nil)
	waitTopic(t, events, "health")
	var added listItem
	for _, r := range e.allModels(map[string]any{"topic": "health"}) {
		if r.Title == "ollama-small" {
			added = r
		} else if r.Text == "subscription" && hasVerb(r.Actions, "remove") {
			t.Fatalf("a built-in provider cannot be removed: %+v", r)
		}
	}
	if !hasVerb(added.Actions, "check") || added.Group != "ollama" {
		t.Fatalf("a model in an ACP agent has a self-check and its button, under its connection: %+v", added)
	}
	remove := verb(t, added.Actions, "remove")
	if remove["confirm"] != true || added.Meta != "agent" {
		t.Fatalf("an added model in an agent says so and is removed after a yes: %+v", added)
	}
	edit := verb(t, added.Actions, "edit")["params"].(map[string]any)
	if edit["dialog"] != "model" || edit["model"] != "small" || edit["connection"] != "ollama" || edit["agent"] != "goose" {
		t.Fatalf("edit opens the dialog of the model filled with its values: %+v", edit)
	}
	var fv formV
	_ = json.Unmarshal(e.screenData("", "settings", map[string]any{"params": `{"connection": "ollama", "model": "small"}`, "dialog": "model"})[formID(t, e, "deck.model_set")]["value"], &fv)
	if fv.Values["connection"] != "ollama" || fv.Values["model"] != "small" {
		t.Fatalf("the form of a model takes its values from the address: %+v", fv.Values)
	}
	e.waitFor("the self-check", func() bool {
		h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:ollama-small")
		return err == nil && h.Status != "checking"
	})
	e.must(remove["command"].(string), remove["params"], nil)
	waitTopic(t, events, "models")
	for _, r := range e.allModels(nil) {
		if r.Title == "ollama-small" {
			t.Fatalf("the removed model is gone: %+v", r)
		}
	}
	if _, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:ollama-small"); err == nil {
		t.Fatal("the health of a removed model goes with it")
	}
}

// TestSettingsHealthShowsWhatIsRedFirstWithWhy: health lists what is broken now with why, and every check in a table
// of subjects with their state and error, the worst first, narrowed by state.
func TestSettingsHealthShowsWhatIsRedFirstWithWhy(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	ctx := t.Context()
	for _, h := range []db.SetHealthParams{{Subject: "mcp:web.search", Status: "ok"}, {Subject: "tool:web.fetch", Status: "error", Error: "the server exited: 1"},
		{Subject: "provider:elsewhere", Status: "error", Error: "a provider of another workspace"}} {
		if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { _, err := q.SetHealth(ctx, h); return err }); err != nil {
			t.Fatal(err)
		}
	}
	block := e.settingsBlock("Everything deck checks", nil)
	rows := blockData[[]screenRow](t, block["rows"])
	if len(rows) < 2 || rows[0].Cells[1] != "error" || rows[0].Details[1] != "the server exited: 1" {
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
	web := store.ID(e.projectID("web"))
	if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(1), ProjectID: web, Subject: "command:web.digest", Status: "error", Error: "no python3"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failing := blockData[[]listItem](t, e.settingsBlock("Broken now", nil)["items"])
	var why []string
	for _, it := range failing {
		why = append(why, it.Text)
	}
	if len(failing) != 2 || !slices.Contains(why, "the server exited: 1") || !slices.Contains(why, "no python3") {
		t.Fatalf("what is broken now comes apart with why, each once, the rest not: %+v", failing)
	}
	webErrors := func() int64 {
		var menu []struct {
			Project string
			Errors  int64
		}
		e.menuOf(&menu)
		for _, m := range menu {
			if m.Project == "web" {
				return m.Errors
			}
		}
		return -1
	}
	unread := func() (n int) {
		var rows []struct {
			Status string
			Seen   bool
		}
		e.must("deck.health_list", map[string]any{}, &rows)
		for _, r := range rows {
			if r.Status == "error" && !r.Seen {
				n++
			}
		}
		return n
	}
	if webErrors() != 1 || unread() != 2 {
		t.Fatalf("a failing check counts until it is read: menu %d, unread %d", webErrors(), unread())
	}
	e.must("deck.health_seen", map[string]any{"subject": "command:web.digest", "project": "web"}, nil)
	if webErrors() != 0 || unread() != 1 {
		t.Fatalf("a read check stops counting, and its project with it: menu %d, unread %d", webErrors(), unread())
	}
	if code, _ := e.call("deck.health_seen", map[string]any{"subject": "mcp:web.search", "project": "web"}); code == 200 {
		t.Fatal("only a failing check is marked read")
	}
	if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(1), ProjectID: web, Subject: "command:web.digest", Status: "error", Error: "no python3.12"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if webErrors() != 1 || unread() != 2 {
		t.Fatalf("a read check counts again once its error changes: menu %d, unread %d", webErrors(), unread())
	}
	id, shown := e.findBlock("", "settings", "Everything deck checks")
	picked := e.screenData("", "settings", merge(shown, map[string]any{"filter": `{"` + id + `": {"status": "ok"}}`}))[id]
	okRows := blockData[[]screenRow](t, picked["rows"])
	for _, r := range okRows {
		if r.Cells[1] != "ok" {
			t.Fatalf("the filter keeps one state: %+v", r)
		}
	}
	if len(okRows) == 0 {
		t.Fatal("the filter keeps the checks of that state")
	}
}

// TestTheDiskHealthReachesTheOpenScreens: a new disk reading tells the clients, so the Health tab is current.
func TestTheDiskHealthReachesTheOpenScreens(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	events := e.sse(t.Context())
	e.App.Sweep(context.Background(), time.Now().Add(time.Hour))
	waitTopic(t, events, "health")
}

// TestAProjectHasItsOwnSettings: deck adds a settings screen to every project: the project with its buttons, its
// commands with their models, its tools, the secrets they use, the tokens that reach it and its health.
func TestAProjectHasItsOwnSettings(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("notes", strings.Replace(notesManifest, `"url": "https://example.invalid/post",`, `"url": "https://example.invalid/post", "headers": {"Authorization": "Bearer $secret.notes_key"},`, 1))
	e.createProject("other", plainManifest)
	_, _ = e.token("mine", "notes.collect")
	_, _ = e.token("theirs", "other.work")
	cells := func(title string) [][]any {
		var out [][]any
		for _, r := range blockData[[]screenRow](t, e.screenBlock("notes", "settings", title, nil)["rows"]) {
			out = append(out, r.Cells)
		}
		return out
	}
	project := blockData[[]listItem](t, e.screenBlock("notes", "settings", "Module", nil)["items"])
	if len(project) != 1 || project[0].Title != "notes" || !strings.HasPrefix(project[0].Meta, "enabled") || !hasVerb(project[0].Actions, "pause") || !hasVerb(project[0].Actions, "delete") {
		t.Fatalf("the settings show the project with its buttons: %+v", project)
	}
	if c := cells("Commands"); len(c) != 1 || c[0][0] != "Collect" {
		t.Fatalf("the commands of the project with the model that does their work: %v", c)
	}
	if c := cells("Tools"); len(c) != 1 || c[0][0] != "notes.post" || c[0][1] != "http" {
		t.Fatalf("the tools of the project: %v", c)
	}
	if c := cells("Secrets"); len(c) != 1 || c[0][0] != "notes_key" || c[0][1] != "not set" {
		t.Fatalf("the secrets its tools use, and whether they are set: %v", c)
	}
	if c := cells("Tokens with access"); len(c) != 1 || c[0][0] != "mine" {
		t.Fatalf("only the tokens that reach the project: %v", c)
	}
}

// TestTheMasterSeesTheNamesOfSecrets: the master lists the names of the secrets, never their values.
func TestTheMasterSeesTheNamesOfSecrets(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "mail-key", "value": "s3cret-value"}, nil)
	out, err := e.App.Registry.Invoke(context.Background(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.secrets_list", []byte(`{}`))
	raw, _ := json.Marshal(out)
	if err != nil || string(raw) != `[{"name":"mail-key","set":true}]` {
		t.Fatalf("the master gets the names of the secrets and no value: %s %v", raw, err)
	}
}

// TestTheMasterReadsTheSettingsAndTheNamesOfTokens: the master reads the settings and the tokens by name and
// commands, so the settings screen it opens has no error in them; a token itself never shows.
func TestTheMasterReadsTheSettingsAndTheNamesOfTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var made struct{ Token string }
	e.must("deck.token_create", map[string]any{"name": "shortcuts", "commands": []string{"deck.notify"}}, &made)
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	for _, name := range []string{"deck.settings_list", "deck.tokens_list"} {
		out, err := e.App.Registry.Invoke(context.Background(), master, name, []byte(`{}`))
		raw, _ := json.Marshal(out)
		if err != nil || strings.Contains(string(raw), made.Token) {
			t.Fatalf("the master reads %s without a token in it: %s %v", name, raw, err)
		}
	}
	out, err := e.App.Registry.Invoke(context.Background(), master, "deck.screen", []byte(`{"screen": "settings"}`))
	if drawn := fmt.Sprint(out); err != nil || strings.Contains(drawn, "settings_list cannot be called") || strings.Contains(drawn, "tokens_list cannot be called") {
		t.Fatalf("the settings screen of the master reads the settings and the tokens: %v %v", err, drawn)
	}
}

// TestEverySettingIsEditedFromItsOneDefinition: settings_set names each setting of the list, and each has the one
// edit button of the General tab, which sets it by its name.
func TestEverySettingIsEditedFromItsOneDefinition(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var list []struct {
		Name    string
		Options []string
	}
	e.must("deck.settings_list", nil, &list)
	var set struct {
		Input struct {
			Properties struct{ Name struct{ Enum []string } }
		}
	}
	e.must("deck.command_describe", map[string]any{"command": "deck.settings_set"}, &set)
	var rows []struct {
		ID      string
		Actions []struct {
			Verb, Command, Input string
			Params               map[string]any
		}
	}
	_ = json.Unmarshal(e.settingsBlock("Limits", nil)["rows"], &rows)
	if len(rows) != len(list)-1 {
		t.Fatalf("the Limits of the health tab list every setting but the language of the interface: %+v", rows)
	}
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	for _, s := range list {
		if !slices.Contains(set.Input.Properties.Name.Enum, s.Name) {
			t.Fatalf("settings_set names %s: %+v", s.Name, set)
		}
		if s.Name == "language" {
			continue
		}
		r := rows[slices.IndexFunc(rows, func(r struct {
			ID      string
			Actions []struct {
				Verb, Command, Input string
				Params               map[string]any
			}
		}) bool {
			return r.ID == s.Name
		})]
		if len(r.Actions) != 1 || r.Actions[0].Verb != "edit" {
			t.Fatalf("%s has the edit button: %+v", s.Name, r)
		}
		if !slices.Contains(s.Options, "2") {
			t.Fatalf("%s offers its values to pick: %+v", s.Name, s.Options)
		}
		in := map[string]any{r.Actions[0].Input: "2"}
		for k, v := range r.Actions[0].Params {
			in[k] = v
		}
		e.must(r.Actions[0].Command, in, nil)
	}
	var after []struct {
		Name  string
		Value any
	}
	e.must("deck.settings_list", nil, &after)
	for _, s := range after {
		if fmt.Sprint(s.Value) != "2" && fmt.Sprint(s.Value) != "open" && fmt.Sprint(s.Value) != "ru" {
			t.Fatalf("the button set %s: %+v", s.Name, after)
		}
	}
	e.refused("deck.settings_set", map[string]any{"name": "agent_limit", "value": "50"}, 422, "1 to 20")
	e.refused("deck.settings_set", map[string]any{"name": "nothing", "value": "1"}, 422, "")
}

// TestTheNamesOfSettingsFollowTheLanguageOfTheScreen: the names of settings come in the language the screen asks for,
// the device's own under auto, whatever the language setting says.
func TestTheNamesOfSettingsFollowTheLanguageOfTheScreen(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	title := func(in map[string]any) string {
		var rows []struct{ Name, Title string }
		e.must("deck.settings_list", in, &rows)
		for _, r := range rows {
			if r.Name == "agent_limit" {
				return r.Title
			}
		}
		return ""
	}
	if got := title(nil); !strings.HasPrefix(got, "Agent processes") {
		t.Fatalf("without a language the names are in English: %q", got)
	}
	if got := title(map[string]any{"lang": "ru"}); !strings.HasPrefix(got, "Сколько агентских процессов") {
		t.Fatalf("the names follow the language asked for: %q", got)
	}
	drawn, _ := json.Marshal(e.screenMessages("", "settings", map[string]any{"lang": "ru", "tab": "Advanced"}))
	if !strings.Contains(string(drawn), "Сколько агентских процессов") {
		t.Fatalf("the settings screen in Russian shows the names in Russian: %s", drawn)
	}
}

// TestASecretDeleteStaysInTheFolderOfSecrets: a name that would leave the folder of the secrets of the workspace is
// refused, and the files there and above stay.
func TestASecretDeleteStaysInTheFolderOfSecrets(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	dir := deckhome.Layout(e.App.Cfg.Home).Secrets(1)
	above := filepath.Join(filepath.Dir(dir), "x")
	if err := os.WriteFile(above, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"..", "...", "../x", "../1/svc", "a/b", ""} {
		e.refused("deck.secret_delete", map[string]any{"name": name}, 422, "")
	}
	for _, f := range []string{above, filepath.Join(dir, "svc")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("the file stays: %v", err)
		}
	}
}

// TestHealthShowsTheModelOfAFailingScheduleAndTheWorkOfAModel: a failing check of a schedule shows the model its
// command runs on with the models it could, and the button that runs it now; a model says what work it does.
func TestHealthShowsTheModelOfAFailingScheduleAndTheWorkOfAModel(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("daily", testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	if err := e.App.Store.Tx(t.Context(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(1), ProjectID: store.ID(e.projectID("daily")), Subject: "trigger:daily.work", Status: "error", Error: "did not start"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var feed []struct{ Check, Model string }
	e.must("deck.feed_list", map[string]any{"kind": "failure"}, &feed)
	if !slices.Contains(feed, struct{ Check, Model string }{"trigger:daily.work", "fake"}) {
		t.Fatalf("a failing schedule says the model it runs on: %+v", feed)
	}
	items := blockData[[]listItem](t, e.settingsBlock("Broken now", nil)["items"])
	i := slices.IndexFunc(items, func(it listItem) bool { return it.Title == "The schedule of daily.work did not start" })
	button := func(command string) map[string]any {
		if i >= 0 {
			for _, a := range items[i].Actions {
				if a["command"] == command {
					return a
				}
			}
		}
		t.Fatalf("a failing schedule has a button of %s: %+v", command, items)
		return nil
	}
	button("deck.trigger_run")
	pick := button("deck.assignment_set")
	if pick["input"] != "model" || !strings.Contains(fmt.Sprint(pick["options"]), "quiet") {
		t.Fatalf("a failing schedule changes its model among those it could run on: %+v", pick)
	}
	params := pick["params"].(map[string]any)
	params["model"] = "quiet"
	e.must("deck.assignment_set", params, nil)
	var hs []struct {
		Subject, Model string
		Places         []string
		Kind           string
	}
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:daily.work" && (h.Model != "quiet" || h.Kind != "trigger") {
			t.Fatalf("the picked model is the one the schedule runs on next: %+v", h)
		}
		if h.Subject == "provider:quiet" && !slices.Contains(h.Places, "daily.work") {
			t.Fatalf("a model says the work it does: %+v", h)
		}
	}
}

// TestLookIsAPlaceOfItsOwn: Look is a screen of deck beside Settings, drawn whole by the stock renderer's rules.
func TestLookIsAPlaceOfItsOwn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	raw, _ := json.Marshal(e.screenMessages("", "look", nil))
	if !strings.Contains(string(raw), `"component":"appearance"`) || !strings.Contains(string(raw), `"topics":[]`) {
		t.Fatalf("Look holds the appearance and names its topics, none: %s", raw)
	}
}

package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/events"
	"deck2/internal/health"
	deckhome "deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice: the look is the client's, so the block
// carries no data, and a project screen cannot hold it.
func TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var menu []struct {
		Project string
		Screens []struct{ Name, Title string }
	}
	e.MenuOf(&menu)
	var names []string
	for _, s := range menu[0].Screens {
		names = append(names, s.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "schedule spend files look settings") {
		t.Fatalf("the deck menu is the schedule, spend, files, look and settings; the cockpit is the link at its head: %v", names)
	}
	check := harness.NewCatalogCheck(t)
	msgs := e.ScreenMessages("", "settings", nil)
	check.Messages(t, msgs)
	var look string
	for id, comp := range check.Comps {
		if comp == "appearance" {
			look = id
		}
	}
	if look == "" {
		t.Fatalf("settings has the appearance block: %v", check.Comps)
	}
	if got := string(e.ScreenData("", "settings", nil)[look]["state"]); got != `"ready"` {
		t.Fatalf("the appearance block is ready without a source: %s", got)
	}
	if drawn, _ := json.Marshal(e.ScreenMessages("", "settings", map[string]any{"topic": "processes"})); strings.Contains(string(drawn), `"/`+look+`"`) {
		t.Fatalf("no event reads the look again: %s", drawn)
	}

	e.Project("web", harness.WebNotes)
	bad := strings.Replace(harness.WebNotes, `{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "appearance"}`, 1)
	code, out := e.Call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a project screen holds no appearance block: %d %s", code, out)
	}
}

// formID is the block id of the deck settings form of a command.
func formID(t *testing.T, e *harness.Env, command string) string {
	t.Helper()
	for _, extra := range append([]map[string]any{nil}, e.DialogsOf("", "settings")...) {
		for id, d := range e.ScreenData("", "settings", extra) {
			if strings.Contains(string(d["value"]), `"name":"`+command+`"`) && strings.Contains(string(d["value"]), `"commands":[{`) {
				return id
			}
		}
	}
	t.Fatalf("no form of %s", command)
	return ""
}

// screenForm is the data of the form of a screen that calls this command, in a dialog or not.
func screenForm(e *harness.Env, project, screen, command string) map[string]json.RawMessage {
	e.T.Helper()
	for _, extra := range append([]map[string]any{nil}, e.DialogsOf(project, screen)...) {
		for _, d := range e.ScreenData(project, screen, extra) {
			if strings.Contains(string(d["value"]), `"commands":[{`) && strings.Contains(string(d["value"]), `"name":"`+command+`"`) {
				return d
			}
		}
	}
	e.T.Fatalf("%s has no form of %s", screen, command)
	return nil
}

func findItem(t *testing.T, items []harness.ListItem, prefix string) harness.ListItem {
	t.Helper()
	for _, it := range items {
		if strings.HasPrefix(it.Title, prefix) {
			return it
		}
	}
	t.Fatalf("no item %q in %+v", prefix, items)
	return harness.ListItem{}
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
	e := harness.New(t, harness.WithAgent(harness.Ok))
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "notes", "manifest": draftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	items := func() []harness.ListItem {
		return harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Modules", nil)["items"])
	}
	press := func(a map[string]any) { t.Helper(); e.Must(a["command"].(string), a["params"], nil) }
	notes := findItem(t, items(), "notes")
	if notes.Title != "notes" || !strings.HasPrefix(notes.Meta, "draft") || strings.Contains(notes.Meta, "autonomy") ||
		!strings.Contains(notes.Meta, "enabling grants: "+strings.Join(e.ProjectRow("notes").Rights, "; ")) {
		t.Fatalf("a draft shows the rights enabling grants: %+v", notes)
	}
	enable := func(review map[string]any) map[string]any {
		t.Helper()
		p := review["params"].(map[string]any)
		params, _ := json.Marshal(map[string]string{"project": fmt.Sprint(p["project"]), "revision": fmt.Sprint(p["revision"])})
		return e.Button("", review["screen"].(string), map[string]any{"params": string(params)}, "deck.project_enable", nil)
	}
	stale := enable(verb(t, notes.Actions, "review"))
	if stale["params"].(map[string]any)["revision"] != float64(e.ManifestRevision("notes")) {
		t.Fatalf("enable on the page of what a draft may do carries the revision the row shows: %+v", stale)
	}

	e.Must("deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"title": "Work"`}, "new": []string{`"title": "Work again"`}}, nil)
	notes = findItem(t, items(), "notes")
	e.Refused(stale["command"].(string), stale["params"], 409, "look at its rights again")
	press(enable(verb(t, notes.Actions, "review")))
	notes = findItem(t, items(), "notes")
	if notes.Meta != "" || hasVerb(notes.Actions, "review") {
		t.Fatalf("an enabled project has no enable button and asks for nothing: %+v", notes)
	}
	press(verb(t, findItem(t, items(), "notes").Actions, "pause"))
	if notes = findItem(t, items(), "notes"); !strings.HasPrefix(notes.Meta, "enabled, paused") || !hasVerb(notes.Actions, "resume") {
		t.Fatalf("a paused project says so and resumes: %+v", notes)
	}
	if f := screenForm(e, "notes", "settings", "deck.command_try"); !strings.Contains(string(f["value"]), `"deck.command_try"`) {
		t.Fatalf("trying a command is a form of the settings of the project: %s", f["value"])
	}
}

// TestSettingsSecretsAndTokensShowOnlyNames: a secret's value is a hidden field and never comes back; a new
// token is shown once, by its form, and the list has only its name and commands.
func TestSettingsSecretsAndTokensShowOnlyNames(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	stream := e.Events(t.Context())
	form := harness.BlockData[harness.FormV](t, screenForm(e, "", "settings", "deck.secret_set")["value"])
	if form.Commands[0].Input.Properties["value"]["writeOnly"] != true {
		t.Fatalf("the value of a secret is a field that hides what is typed: %+v", form.Commands[0].Input.Properties)
	}
	e.Must("deck.secret_set", map[string]any{"name": "mail-key", "value": "s3cret-value"}, nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "secrets"})
	secrets := e.SettingsBlock("Secrets", map[string]any{"topic": "secrets"})
	got := harness.BlockData[[]harness.ScreenRow](t, secrets["rows"])
	if len(got) != 1 || got[0].Cells[0] != "mail-key" || !hasVerb(got[0].Actions, "replace") || verb(t, got[0].Actions, "delete")["confirm"] != true {
		t.Fatalf("the secrets tab lists names with replace and delete, and follows the secrets topic: %s", secrets["rows"])
	}
	del := verb(t, got[0].Actions, "delete")
	e.Must(del["command"].(string), del["params"], nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "secrets"})
	if left := harness.BlockData[[]harness.ScreenRow](t, e.SettingsBlock("Secrets", nil)["rows"]); len(left) != 0 {
		t.Fatalf("a deleted secret is gone: %+v", left)
	}

	var made struct {
		Token      string
		ClaudeCode string `json:"claude_code"`
	}
	e.Must("deck.token_create", map[string]any{"name": "shortcuts", "commands": []string{"deck.notify", "web.*"}}, &made)
	harness.WaitEvent(t, stream, events.Event{Topic: "tokens"})
	all, _ := json.Marshal(e.ScreenData("", "settings", nil))
	if strings.Contains(string(all), "s3cret-value") || strings.Contains(string(all), made.Token) || !strings.Contains(made.ClaudeCode, made.Token) {
		t.Fatalf("no secret value or token is on the screen after it was made: %s", all)
	}
	rows := harness.BlockData[[]harness.ScreenRow](t, e.SettingsBlock("Automation tokens", map[string]any{"topic": "tokens"})["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != "shortcuts" || rows[0].Cells[2] != "deck.notify, web.*" || rows[0].Cells[1] != "live" {
		t.Fatalf("a token shows its name, commands and state: %+v", rows)
	}
	revoke := verb(t, rows[0].Actions, "revoke")
	if revoke["confirm"] != true {
		t.Fatalf("revoking asks the owner first: %+v", revoke)
	}
	e.Must(revoke["command"].(string), revoke["params"], nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "tokens"})
	rows = harness.BlockData[[]harness.ScreenRow](t, e.SettingsBlock("Automation tokens", nil)["rows"])
	if rows[0].Cells[1] != "revoked" || hasVerb(rows[0].Actions, "revoke") {
		t.Fatalf("a revoked token stays in the list without the button: %+v", rows)
	}
	if code, _ := e.As(made.Token).Call("deck.notify", map[string]any{"text": "hi"}); code != 401 {
		t.Fatalf("the revoked token no longer calls: %d", code)
	}
	var journal []map[string]any
	e.Must("deck.calls_list", nil, &journal)
	if len(journal) != 1 || journal[0]["tool"] != "deck.token_revoke" || journal[0]["class"] != "external" || journal[0]["actor"] != "owner" {
		t.Fatalf("revoking a token is an external call in the journal: %+v", journal)
	}
	var revoked harness.CommandView
	e.Must("deck.command_describe", map[string]any{"command": "deck.token_revoke"}, &revoked)
	if revoked.Class != "external" {
		t.Fatalf("revoking a token is external: %q", revoked.Class)
	}
	_, other := e.Token("terminal", "deck.*")
	if code, _ := e.As(other).Call("deck.calls_list", nil); code != 403 {
		t.Fatalf("only the owner reads the journal: %d", code)
	}
}

// TestSettingsModelsAddAndRemoveOnTheScreen: the models tab adds a model with a form and removes an added one after
// the owner says yes; built-in providers have no remove.
func TestSettingsModelsAddAndRemoveOnTheScreen(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	e := harness.New(t, harness.WithConfig(func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
		c.Sandbox = harness.FakeDirSandbox{Sandbox: sandbox.New(), Dir: f.Dir, More: nil}
	}))

	stream := e.Events(t.Context())
	if f := screenForm(e, "", "settings", "deck.model_set"); !strings.Contains(string(f["value"]), `"deck.model_set"`) {
		t.Fatalf("the models tab adds a model with a form: %s", f["value"])
	}
	e.Must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": "http://127.0.0.1:1/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "ollama", "model": "small", "name": "ollama-small", "agent": "goose"}, nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "health"})
	var added harness.ListItem
	for _, r := range e.AllModels(map[string]any{"topic": "health"}) {
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
	var fv harness.FormV
	_ = json.Unmarshal(e.ScreenData("", "settings", map[string]any{"params": `{"connection": "ollama", "model": "small"}`, "dialog": "model"})[formID(t, e, "deck.model_set")]["value"], &fv)
	if fv.Values["connection"] != "ollama" || fv.Values["model"] != "small" {
		t.Fatalf("the form of a model takes its values from the address: %+v", fv.Values)
	}
	e.Until("the self-check", func() bool {
		h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:ollama-small")
		return err == nil && h.Status != "checking"
	})
	e.Must(remove["command"].(string), remove["params"], nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "models"})
	for _, r := range e.AllModels(nil) {
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
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	ctx := t.Context()
	for _, h := range []db.SetHealthParams{{Subject: "mcp:web.search", Status: "ok"}, {Subject: "tool:web.fetch", Status: "error", Error: "the server exited: 1"},
		{Subject: "provider:elsewhere", Status: "error", Error: "a provider of another workspace"}} {
		if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { _, err := q.SetHealth(ctx, h); return err }); err != nil {
			t.Fatal(err)
		}
	}
	block := e.SettingsBlock("Everything deck checks", nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, block["rows"])
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
	web := store.ID(e.ProjectID("web"))
	if err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(1), ProjectID: web, Subject: "command:web.digest", Status: "error", Error: "no python3"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failing := harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Broken now", nil)["items"])
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
		e.MenuOf(&menu)
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
		e.Must("deck.health_list", map[string]any{}, &rows)
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
	e.Must("deck.health_seen", map[string]any{"subject": "command:web.digest", "project": "web"}, nil)
	if webErrors() != 0 || unread() != 1 {
		t.Fatalf("a read check stops counting, and its project with it: menu %d, unread %d", webErrors(), unread())
	}
	if code, _ := e.Call("deck.health_seen", map[string]any{"subject": "mcp:web.search", "project": "web"}); code == 200 {
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
	id, shown := e.FindBlock("", "settings", "Everything deck checks")
	picked := e.ScreenData("", "settings", harness.Merge(shown, map[string]any{"filter": `{"` + id + `": {"status": "ok"}}`}))[id]
	okRows := harness.BlockData[[]harness.ScreenRow](t, picked["rows"])
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
	e := harness.New(t)
	stream := e.Events(t.Context())
	e.App.Sweep(context.Background(), time.Now().Add(time.Hour))
	harness.WaitEvent(t, stream, events.Event{Topic: "health"})
}

// TestAProjectHasItsOwnSettings: deck adds a settings screen to every project: the project with its buttons, its
// commands with their models, its tools, the secrets they use, the tokens that reach it and its health.
func TestAProjectHasItsOwnSettings(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", strings.Replace(notesManifest, `"url": "https://example.invalid/post",`, `"url": "https://example.invalid/post", "headers": {"Authorization": "Bearer $secret.notes_key"},`, 1))
	e.Project("other", harness.PlainManifest)
	_, _ = e.Token("mine", "notes.collect")
	_, _ = e.Token("theirs", "other.work")
	cells := func(title string) [][]any {
		var out [][]any
		for _, r := range harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("notes", "settings", title, nil)["rows"]) {
			out = append(out, r.Cells)
		}
		return out
	}
	project := harness.BlockData[[]harness.ListItem](t, e.ScreenBlock("notes", "settings", "Module", nil)["items"])
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
	e := harness.New(t)
	e.Must("deck.secret_set", map[string]any{"name": "mail-key", "value": "s3cret-value"}, nil)
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
	e := harness.New(t)
	var made struct{ Token string }
	e.Must("deck.token_create", map[string]any{"name": "shortcuts", "commands": []string{"deck.notify"}}, &made)
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
	e := harness.New(t)
	var list []struct {
		Name    string
		Options []string
	}
	e.Must("deck.settings_list", nil, &list)
	var set struct {
		Input struct {
			Properties struct{ Name struct{ Enum []string } }
		}
	}
	e.Must("deck.command_describe", map[string]any{"command": "deck.settings_set"}, &set)
	var rows []struct {
		ID      string
		Actions []struct {
			Verb, Command, Input string
			Params               map[string]any
		}
	}
	_ = json.Unmarshal(e.SettingsBlock("Limits", nil)["rows"], &rows)
	if len(rows) != len(list)-1 {
		t.Fatalf("the Limits of the health tab list every setting but the language of the interface: %+v", rows)
	}
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
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
		e.Must(r.Actions[0].Command, in, nil)
	}
	var after []struct {
		Name  string
		Value any
	}
	e.Must("deck.settings_list", nil, &after)
	for _, s := range after {
		if fmt.Sprint(s.Value) != "2" && fmt.Sprint(s.Value) != "open" && fmt.Sprint(s.Value) != "ru" {
			t.Fatalf("the button set %s: %+v", s.Name, after)
		}
	}
	e.Refused("deck.settings_set", map[string]any{"name": "agent_limit", "value": "50"}, 422, "1 to 20")
	e.Refused("deck.settings_set", map[string]any{"name": "nothing", "value": "1"}, 422, "")
}

// TestTheNamesOfSettingsFollowTheLanguageOfTheScreen: the names of settings come in the language the screen asks for,
// the device's own under auto, whatever the language setting says.
func TestTheNamesOfSettingsFollowTheLanguageOfTheScreen(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	title := func(in map[string]any) string {
		var rows []struct{ Name, Title string }
		e.Must("deck.settings_list", in, &rows)
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
	drawn, _ := json.Marshal(e.ScreenMessages("", "settings", map[string]any{"lang": "ru", "tab": "Advanced"}))
	if !strings.Contains(string(drawn), "Сколько агентских процессов") {
		t.Fatalf("the settings screen in Russian shows the names in Russian: %s", drawn)
	}
}

// TestASecretDeleteStaysInTheFolderOfSecrets: a name that would leave the folder of the secrets of the workspace is
// refused, and the files there and above stay.
func TestASecretDeleteStaysInTheFolderOfSecrets(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	dir := deckhome.Layout(e.App.Cfg.Home).Secrets(1)
	above := filepath.Join(filepath.Dir(dir), "x")
	if err := os.WriteFile(above, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"..", "...", "../x", "../1/svc", "a/b", ""} {
		e.Refused("deck.secret_delete", map[string]any{"name": name}, 422, "")
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
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("daily", harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	if err := e.App.Store.Tx(t.Context(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(1), ProjectID: store.ID(e.ProjectID("daily")), Subject: "trigger:daily.work", Status: "error", Error: "did not start"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var feed []struct{ Check, Model string }
	e.Must("deck.feed_list", map[string]any{"kind": "failure"}, &feed)
	if !slices.Contains(feed, struct{ Check, Model string }{"trigger:daily.work", "fake"}) {
		t.Fatalf("a failing schedule says the model it runs on: %+v", feed)
	}
	items := harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Broken now", nil)["items"])
	i := slices.IndexFunc(items, func(it harness.ListItem) bool { return it.Title == "The schedule of daily.work did not start" })
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
	e.Must("deck.assignment_set", params, nil)
	var hs []struct {
		Subject, Model string
		Places         []string
		Kind           string
	}
	e.Must("deck.health_list", nil, &hs)
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
	e := harness.New(t)
	raw, _ := json.Marshal(e.ScreenMessages("", "look", nil))
	if !strings.Contains(string(raw), `"component":"appearance"`) || !strings.Contains(string(raw), `"topics":[]`) {
		t.Fatalf("Look holds the appearance and names its topics, none: %s", raw)
	}
}

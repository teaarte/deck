package modules

import (
	"context"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestManifestIsValidatedOnSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(context.Context, *harness.Call) provider.Result { return provider.Result{} }))
	bad := strings.NewReplacer(`"collection": "ideas", "where"`, `"collection": "nope", "where"`, `"0 9 * * *"`, `"every day"`).Replace(harness.IdeasManifest)
	code, out := e.Call("deck.project_create", map[string]any{"name": "bad", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "unknown collection") || !strings.Contains(string(out), "cron") {
		t.Fatalf("want 422 with reasons, got %d %s", code, out)
	}
}

// TestAManifestIsCheckedWithoutASave: manifest_check says what a save would refuse, warn of and widen, and saves
// nothing; a button that only opens the chat is refused, as the button of Deki opens it.
func TestAManifestIsCheckedWithoutASave(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	var before struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": "web"}, &before)
	chat := strings.Replace(harness.WebNotes, `"screens": [`, `"screens": [{"name": "inbox", "title": "Inbox", "blocks": [{"type": "list", "source": {"collection": "notes"},
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
	wider := strings.Replace(harness.WebNotes, harness.Manifest(`{`), harness.Manifest(`{"domains": {"read": ["example.com"]},`), 1)
	e.Must("deck.manifest_check", map[string]any{"project": "web", "manifest": wider}, &c)
	if !c.Ok || len(c.Widens) == 0 {
		t.Fatalf("the check says what a manifest widens: %+v", c)
	}
	e.Must("deck.manifest_check", map[string]any{"manifest": harness.Manifest(`{"commands": [{"name": "x"}]}`)}, &c)
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

var fullManifest = harness.Manifest(`{
  "collections": [
    {"name": "sources", "description": "A page to watch", "key": "url", "display": "url",
     "schema": {"type": "object", "properties": {"url": {"type": "string"}, "status": {"type": "string"}}, "required": ["url"]}},
    {"name": "links", "description": "A link found on a source", "key": "url", "display": "title + ' (' + site + ')'",
     "immutable": ["found_at"], "retention_days": 90,
     "schema": {"type": "object", "properties": {"url": {"type": "string"}, "title": {"type": "string"}, "site": {"type": "string"},
       "score": {"type": "integer"}, "found_at": {"type": "string"}}, "required": ["url", "title"]}}
  ],
  "commands": [
    {"name": "collect", "title": "Collect links", "description": "Reads every source and stores new links.", "class": "internal",
     "result": {"type": ["object", "null"]}, "timeout_minutes": 30,
     "executor": {"type": "agent", "prompt_file": "prompts/collect.md", "skills": ["reader"]}},
    {"name": "parse", "title": "Parse a source", "description": "Parses one source page with a script.", "class": "internal",
     "input": {"type": "object", "properties": {"source": {"type": "integer", "format": "record"}}, "required": ["source"]},
     "callers": ["agent", "owner"], "executor": {"type": "script", "script": "parse"}},
    {"name": "rate", "title": "Rate links", "description": "Scores new links.", "class": "internal",
     "input": {"type": "object", "properties": {"limit": {"type": "integer"},
       "process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Score the new links 1-10."}},
    {"name": "share", "title": "Share a link", "description": "Posts a link to the owner's feed service.", "class": "external",
     "input": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]},
     "executor": {"type": "tool", "tool": "post_link"}}
  ],
  "scripts": [{"name": "parse", "file": "scripts/parse.py", "runtime": "python", "timeout_minutes": 5}],
  "triggers": [
    {"command": "collect", "cron": "0 8 * * *"},
    {"command": "rate", "after": "collect", "params": {"limit": 20}}
  ],
  "tools": {
    "http": [{"name": "post_link", "description": "Posts a link", "method": "POST", "url": "https://feed.example.com/api/links",
      "headers": {"X-Api-Key": "$secret.feed"}, "body": {"url": "{url}"}, "class": "external", "confirm_ttl_minutes": 60,
      "input": {"type": "object", "properties": {"url": {"type": "string"}}}}],
    "mcp": {"servers": {"browser": {"command": "npx", "args": ["@playwright/mcp"], "toolFilter": ["browser_navigate", "browser_snapshot"],
      "classes": {"browser_navigate": "internal", "browser_snapshot": "internal"}}}}
  },
  "allowed_external": ["post_link"],
  "domains": {"read": ["news.example.com"], "act": []},
  "screens": [{"name": "main", "title": "Links", "blocks": [{"type": "table", "title": "Best",
    "source": {"collection": "links", "where": "score >= 7", "sort": "-score", "limit": 20},
    "columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
    "actions": [{"label": "Share", "command": "share", "params": {"url": "=url"}}]}]}],
  "menu": ["main"]
}`)

func TestFullManifestIsAccepted(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	var schema map[string]any
	e.Must("deck.manifest_format", nil, &schema)
	if schema["title"] != "deck project manifest" {
		t.Fatalf("manifest_format must return the schema, got %v", schema["title"])
	}
	if b, _ := json.Marshal(schema["$defs"].(map[string]any)["block"]); !strings.Contains(string(b), `"required":["source","columns"]`) ||
		!strings.Contains(string(b), "never both") || !strings.Contains(string(b), "Rows of a source.") {
		t.Fatalf("the model reads the form of each block of the catalog and its rules in words: %s", b)
	}
	found := false
	for _, c := range e.Commands() {
		if c.Name == "links.parse" {
			found = strings.Contains(string(c.Input), `"record"`)
		}
	}
	if !found {
		t.Fatalf("project command must carry its declared input schema")
	}
}

// TestAFixedParamOfAButtonTakesTheTypeOfItsField: params of a button are text in the manifest; a fixed one
// reaches the command as the integer its input field is, so the owner's press is not refused.
func TestAFixedParamOfAButtonTakesTheTypeOfItsField(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", strings.Replace(fullManifest, `{"label": "Share", "command": "share", "params": {"url": "=url"}}`,
		`{"label": "Share", "command": "share", "params": {"url": "=url"}}, {"label": "Parse", "command": "parse", "params": {"source": "7"}}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "u", "title": "t", "score": 8}`}, nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenData("links", "main", nil)["b0"]["rows"])
	if len(rows) != 1 || len(rows[0].Actions) != 2 {
		t.Fatalf("the row has both buttons: %+v", rows)
	}
	parse := rows[0].Actions[1]
	if parse["params"].(map[string]any)["source"] != float64(7) {
		t.Fatalf("a fixed param goes out as the integer of its field: %+v", parse)
	}
	if code, out := e.Call("links.parse", parse["params"]); code == 422 && strings.Contains(string(out), "want integer") {
		t.Fatalf("the press of the button matches the input of its command: %s", out)
	}
}

func TestManifestReferencesAreChecked(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("other", harness.IdeasManifest)
	cases := []struct{ name, old, new, want string }{
		{"unknown field", `"menu": ["main"]`, `"menu": ["main"], "colour": "red"`, "colour"},
		{"trigger cycle", `{"command": "rate", "after": "collect", "params": {"limit": 20}}`,
			`{"command": "rate", "after": "collect", "params": {"limit": 20}}, {"command": "collect", "after": "rate"}`, "cycle: collect → rate → collect"},
		{"trigger params", `"params": {"limit": 20}`, `"params": {"limit": "many"}`, "params do not match"},
		{"nested input", `"properties": {"limit": {"type": "integer"},`, `"properties": {"limit": {"type": "object"},`, "App Intents"},
		{"after input", `"limit": {"type": "integer"},
       "process": {"type": "integer", "format": "process"}}`, `"limit": {"type": "integer"}}`, `must declare input field "process"`},
		{"unknown script", `"script": "parse"}`, `"script": "scrape"}`, `unknown script "scrape"`},
		{"unknown tool", `"tool": "post_link"`, `"tool": "post_it"`, `unknown tool "post_it"`},
		{"unknown external", `"allowed_external": ["post_link"]`, `"allowed_external": ["mail"]`, `"mail" is neither`},
		{"key field", `"key": "url", "display": "title`, `"key": "link", "display": "title`, `unknown field "link"`},
		{"where field", `"where": "score >= 7"`, `"where": "rating >= 7"`, `unknown field "rating"`},
		{"sort field", `"sort": "-score"`, `"sort": "-rank"`, `unknown field "rank"`},
		{"action field", `"params": {"url": "=url"}`, `"params": {"url": "=href"}`, `unknown field "href"`},
		{"immutable field", `"immutable": ["found_at"]`, `"immutable": ["seen_at"]`, `unknown field "seen_at"`},
		{"menu", `"menu": ["main"]`, `"menu": ["home"]`, `unknown screen "home"`},
		{"unknown function", `"display": "title + ' (' + site + ')'"`, `"display": "shout(title)"`, `unknown function "shout"`},
		{"action params", `"params": {"url": "=url"}`, `"params": {"link": "=url"}`, `needs param "url"`},
		{"reads project", `"menu": ["main"]`, `"menu": ["main"], "reads": [{"project": "nope"}]`, `no project "nope"`},
		{"reads collection", `"menu": ["main"]`, `"menu": ["main"], "reads": [{"project": "other", "collections": ["nope"]}]`, `no collection "nope"`},
		{"trigger caller", `"callers": ["agent", "owner"], "executor": {"type": "script"`, `"callers": ["agent"], "executor": {"type": "script"`, ""},
	}
	for i, c := range cases {
		m := strings.Replace(fullManifest, c.old, c.new, 1)
		if m == fullManifest {
			t.Fatalf("%s: fixture replacement did not apply", c.name)
		}
		code, out := e.Call("deck.project_create", map[string]any{"name": fmt.Sprintf("links%d", i), "manifest": m})
		if c.want == "" {
			if code != 200 {
				t.Fatalf("%s: want accepted, got %d %s", c.name, code, out)
			}
			continue
		}
		if code != 422 || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: want 422 containing %q, got %d %s", c.name, c.want, code, out)
		}
	}
	m := strings.Replace(fullManifest, `"menu": ["main"]`, `"menu": ["main"], "reads": [{"project": "other", "collections": ["ideas"]}]`, 1)
	e.Project("links", m)
}

func TestScriptCommandNeedsItsFileAndInput(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	if code, out := e.Call("links.parse", map[string]any{"source": 1}); code != 422 || !strings.Contains(string(out), "scripts/parse.py") {
		t.Fatalf("a script command without its script file is refused: %d %s", code, out)
	}
	if code, out := e.Call("links.parse", map[string]any{}); code != 422 || !strings.Contains(string(out), "source") {
		t.Fatalf("input schema must be enforced: %d %s", code, out)
	}
}

func TestPromptFileAndSkillsFeedTheAgent(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))

	p := e.Project("links", fullManifest)
	if code, out := e.Call("links.collect", nil); code != 422 || !strings.Contains(string(out), "prompts/collect.md") {
		t.Fatalf("missing prompt file must be refused, got %d %s", code, out)
	}
	e.Must("deck.document_save", map[string]any{"project": "links", "name": "prompts/collect.md", "kind": "doc", "text": "Visit each source slowly."}, nil)
	if code, out := e.Call("links.collect", nil); code != 422 || !strings.Contains(string(out), `skill "reader" is missing`) {
		t.Fatalf("missing skill must be refused, got %d %s", code, out)
	}
	skill := "---\nname: reader\ndescription: Reads pages.\n---\nRead slowly.\n"
	e.Must("deck.document_save", map[string]any{"name": "skills/reader/SKILL.md", "kind": "skill", "text": skill}, nil)
	e.Must("deck.document_save", map[string]any{"name": "skills/reader/notes/sites.md", "kind": "skill", "text": "example.com"}, nil)
	got := e.WaitState(e.Run(p, "collect"), "done")
	call := e.Fake.Calls()[0]
	if got.NotBySchema || !strings.Contains(call.Prompt, "Visit each source slowly.") {
		t.Fatalf("prompt file must reach the agent and null must pass the schema: %+v", got)
	}
	if call.Skills["reader/SKILL.md"] != skill || call.Skills["reader/notes/sites.md"] != "example.com" || len(call.Skills) != 2 {
		t.Fatalf("the files of the command's skills must reach the agent: %v", call.Skills)
	}
}

func TestSchemaChangesAreCheckedAgainstRecords(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	var lines []map[string]any
	for i := range 25 {
		lines = append(lines, map[string]any{"url": fmt.Sprintf("https://x.example/%d", i), "title": fmt.Sprintf("t%d", i), "found_at": "2026-09-25"})
	}
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": harness.Jsonl(lines...)}, nil)

	compatible := strings.Replace(fullManifest, `"found_at": {"type": "string"}}`, `"found_at": {"type": "string"}, "note": {"type": "string"}}`, 1)
	e.Must("deck.manifest_save", map[string]any{"project": "links", "manifest": compatible}, nil)

	breaking := strings.Replace(compatible, `"required": ["url", "title"]`, `"required": ["url", "title", "note"]`, 1)
	code, out := e.Call("deck.manifest_save", map[string]any{"project": "links", "manifest": breaking})
	msg := string(out)
	if code != 422 || !strings.Contains(msg, "25 of 25 records") || strings.Count(msg, "  - record") != 20 || !strings.Contains(msg, "migration script") {
		t.Fatalf("breaking change must be refused with the first 20 records and the total, got %d %s", code, msg)
	}

	rekey := strings.Replace(compatible, `"key": "url", "display": "title`, `"key": "title", "display": "title`, 1)
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "links", "manifest": rekey}); code != 422 || !strings.Contains(string(out), "key would change") {
		t.Fatalf("key change must be refused, got %d %s", code, out)
	}
	sameKey := strings.Replace(compatible, `"key": "url", "display": "title`, `"key": "site", "display": "title`, 1)
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "links", "manifest": sameKey}); code != 422 || !strings.Contains(string(out), "key") {
		t.Fatalf("key that breaks every record must be refused, got %d %s", code, out)
	}

	var cur struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "links"}, &cur)
	if cur.Manifest != manifest.Canonical(compatible) {
		t.Fatalf("refused revisions must leave the manifest unchanged")
	}

	removed := strings.Replace(strings.Replace(compatible, `"collection": "links"`, `"collection": "sources"`, 1), `"where": "score >= 7", "sort": "-score"`, `"sort": "url"`, 1)
	removed = strings.Replace(removed, `{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}`, `{"label": "URL", "bind": "url"}`, 1)
	removed = strings.Replace(removed, `"params": {"url": "=url"}`, `"params": {"url": "=url"}`, 1)
	start := strings.Index(removed, `    {"name": "links"`)
	end := strings.Index(removed, "\n  ],\n  \"commands\"")
	removed = removed[:start-2] + removed[end:]
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "links", "manifest": removed}); code != 422 || !strings.Contains(string(out), "has 25 records") {
		t.Fatalf("removing a collection with records must be refused, got %d %s", code, out)
	}
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.collection_delete", map[string]any{"project": "links", "collection": "links"}); code != 200 ||
		!strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("the master side the refusal tells to delete the records asks the owner to: %d %s", code, out)
	}
	e.Answer(e.WaitAsk().ID, "allow")
	e.Until("the records deleted", func() bool {
		var recs []any
		e.Must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
		return len(recs) == 0
	})
	e.Must("deck.manifest_save", map[string]any{"project": "links", "manifest": removed}, nil)
}

func TestImmutableFieldsCannotChange(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	rec := map[string]any{"url": "https://x.example/1", "title": "one", "found_at": "2026-09-25"}
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": harness.Jsonl(rec)}, nil)

	rec["found_at"] = "2026-09-26"
	var up struct {
		Written int
		Errors  []string
	}
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": harness.Jsonl(rec)}, &up)
	if up.Written != 0 || len(up.Errors) != 1 || !strings.Contains(up.Errors[0], "found_at") {
		t.Fatalf("upsert must not change an immutable field: %+v", up)
	}
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	b, _ := json.Marshal(rec)
	if code, out := e.Call("deck.record_update", map[string]any{"record": recs[0].ID, "data": string(b)}); code != 422 || !strings.Contains(string(out), "found_at") {
		t.Fatalf("record_update must not change an immutable field: %d %s", code, out)
	}
	rec["found_at"], rec["title"] = "2026-09-25", "one, edited"
	b, _ = json.Marshal(rec)
	e.Must("deck.record_update", map[string]any{"record": recs[0].ID, "data": string(b)}, nil)
}

func TestKeyFieldsStayAndEmptyTriggersAreRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	contacts := harness.Manifest(`{"collections": [{"name": "contacts", "description": "People", "key": "lower(email)",
	    "schema": {"type": "object", "properties": {"email": {"type": "string"}, "big": {"type": "integer"}}, "required": ["email"]}}],
	  "commands": [{"name": "fetch-all", "title": "Fetch", "description": "Fetches <all> & more", "class": "internal",
	    "executor": {"type": "agent", "prompt": "Fetch."}}]}`)
	e.Project("contacts", contacts)
	var cur struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "contacts"}, &cur)
	if !strings.Contains(cur.Manifest, "<all> & more") {
		t.Fatalf("a stored manifest keeps its text as is:\n%s", cur.Manifest)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "contacts", "collection": "contacts", "records": `{"email": "A@x.example"}`}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "contacts", "collection": "contacts"}, &recs)
	code, out := e.Call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"email": "a@x.example"}`})
	if code != 422 || !strings.Contains(string(out), `"email"`) {
		t.Fatalf("a field of the key expression must stay immutable, got %d %s", code, out)
	}
	for _, bad := range []string{`"cron": ""`, `"after": ""`} {
		m := strings.Replace(fullManifest, `"cron": "0 8 * * *"`, bad, 1)
		if code, _ := e.Call("deck.project_create", map[string]any{"name": "empty", "manifest": m}); code != 422 {
			t.Errorf("trigger with %s must be refused, got %d", bad, code)
		}
	}
}

// TestAManifestOfAnyFormatButThirteenIsRefused: deck reads only the current format and says which version it got.
func TestAManifestOfAnyFormatButThirteenIsRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, v := range []string{"12", "4", "3"} {
		m := harness.ManifestVersion(harness.IdeasManifest, v)
		e.Refused("deck.project_create", map[string]any{"name": "old" + v, "manifest": m}, 422, fmt.Sprintf("manifest: version must be the number %d, got %s", manifest.FormatVersion, v))
	}
	e.Project("ideas", harness.IdeasManifest)
}

// TestOnlyTheBuiltInProjectAnswersEventsOfDeck: a trigger on a message or a failure belongs to project master.
func TestOnlyTheBuiltInProjectAnswersEventsOfDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.PlainManifest, `"triggers": [],`, `"triggers": [{"command": "work", "on": "failure"}],`, 1)
	m = strings.Replace(m, `"executor": {"type": "agent", "prompt": "Do the work."}`,
		`"callers": ["trigger"], "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}}, "executor": {"type": "agent", "prompt": "Do the work."}`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "spy", "manifest": m}, 422, "belongs to the built-in project master")
}

// TestAManifestWithMasterEventsIsRefused: deck has no master_events; a manifest naming it does not match the format.
func TestAManifestWithMasterEventsIsRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := harness.ManifestWith(harness.PlainManifest, `"master_events": true`)
	if m == harness.PlainManifest {
		t.Fatal("fixture replacement did not apply")
	}
	e.Refused("deck.project_create", map[string]any{"name": "old", "manifest": m}, 422, "master_events")
}

func TestProjectPromptReachesTheAgent(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	var p struct{ ID int64 }
	e.Must("deck.project_create", map[string]any{"name": "ideas", "manifest": harness.IdeasManifest, "prompt": "Ideas for a solo developer.\nPrefer Go."}, &p)
	e.Names[p.ID] = "ideas"
	e.WaitState(e.Run(p.ID, "collect"), "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "Prefer Go.") {
		t.Fatalf("project prompt must be part of the agent prompt, got %+v", calls)
	}
}

func TestExpressionVariablesCannotHideFields(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	key := strings.Replace(harness.CalcManifest, `"key": "n"`, `"key": "string(n) + (let n = 1; string(n))"`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "a", "manifest": key}, 422, "not allowed in a key")
	display := strings.Replace(harness.CalcManifest, `"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "b", "manifest": display}, 422, `hides the field`)
}

func TestVariablesNeedKnownFields(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	m := strings.NewReplacer(`"schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}`, `"schema": {"type": "object"}`,
		`"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`).Replace(harness.CalcManifest)
	e.Refused("deck.project_create", map[string]any{"name": "a", "manifest": m}, 422, "need a schema that lists its properties")
}

// TestADeclaredParamFillsAFormAndActionsHaveNoMenu: =param.<name> of a declared param fills the values of a form;
// a group of buttons with a menu is refused.
func TestADeclaredParamFillsAFormAndActionsHaveNoMenu(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	form := strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"title": "string"}, "blocks": [
      {"type": "form", "title": "Again", "command": "score", "values": {"title": "=param.title"}},`, 1)
	if form == harness.WebNotes {
		t.Fatal("fixture replacement did not apply")
	}
	e.Project("web", form)
	data := e.ScreenData("web", "main", map[string]any{"params": `{"title": "alpha"}`})
	if !strings.Contains(string(data["b0"]["value"]), `"values":{"title":"alpha"}`) {
		t.Fatalf("the declared param fills the form: %s", data["b0"]["value"])
	}
	menu := strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "actions", "title": "More", "menu": true, "actions": [{"label": "Tell", "command": "tell"}]},`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": menu}, 422, "menu")
}

// TestAVersionThatIsNoNumberSaysSo: a version written as text or a fraction is refused with what it is.
func TestAVersionThatIsNoNumberSaysSo(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for i, c := range []struct{ version, want string }{{`"6"`, `the text "6"`}, {`6.0`, `6.0`}} {
		m := harness.ManifestVersion(harness.WebNotes, c.version)
		code, out := e.Call("deck.project_create", map[string]any{"name": fmt.Sprintf("v%d", i), "manifest": m})
		if code != 422 || !strings.Contains(string(out), c.want) || !strings.Contains(string(out), fmt.Sprintf("the number %d", manifest.FormatVersion)) {
			t.Errorf("%s: want 422 naming %s, got %d %s", c.version, c.want, code, out)
		}
	}
}

// TestAnInputThatGivesNothingIsAnErrorOfItsBlock: a source whose input resolves to nothing or fails shows an error
// on its block, not a read without that filter; a button whose when fails says so too.
func TestAnInputThatGivesNothingIsAnErrorOfItsBlock(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", harness.IdeasManifest)
	e.WaitState(e.Start("ideas", "collect", nil), "done", "error")
	runs := strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"project": "string"}, "blocks": [
      {"type": "table", "title": "Runs", "source": {"from": "deck.processes_list", "input": {"project": "=param.project"}}, "columns": [{"label": "Command", "bind": "command"}]},
      {"type": "table", "title": "Bad", "source": {"from": "deck.processes_list", "input": {"project": "=upper(param.project) + 1"}}, "columns": [{"label": "Command", "bind": "command"}]},
      {"type": "actions", "title": "Do", "actions": [{"label": "Tell", "command": "tell", "when": "param.project + 1 > 0"}]},
      {"type": "table", "title": "Each", "source": {"from": "deck.processes_list", "input": {"project": "web"}}, "columns": [{"label": "Command", "bind": "command"}],
        "actions": [{"label": "Again", "command": "tell", "when": "ask > 5"}]},`, 1)
	if runs == harness.WebNotes {
		t.Fatal("fixture replacement did not apply")
	}
	e.Project("web", runs)
	data := e.ScreenData("web", "main", map[string]any{"params": `{"project": "web"}`})
	if string(data["b0"]["state"]) != `"empty"` && string(data["b0"]["state"]) != `"ready"` || strings.Contains(string(data["b0"]["value"]), "collect") {
		t.Fatalf("with its param the block reads the runs of that project only: %s", data["b0"])
	}
	data = e.ScreenData("web", "main", nil)
	if string(data["b0"]["state"]) != `"error"` || !strings.Contains(string(data["b0"]["error"]), "project") {
		t.Fatalf("without its param the block is an error, not a read without the filter: %s", data["b0"])
	}
	data = e.ScreenData("web", "main", map[string]any{"params": `{"project": "web"}`})
	if string(data["b1"]["state"]) != `"error"` {
		t.Fatalf("an input whose expression fails is an error of its block: %s", data["b1"])
	}
	if string(data["b2"]["state"]) != `"error"` {
		t.Fatalf("a button whose when fails is an error of its block, not a button that is quietly gone: %s", data["b2"])
	}
	e.WaitState(e.Start("web", "tell", nil), "done", "error")
	data = e.ScreenData("web", "main", map[string]any{"params": `{"project": "web"}`})
	if string(data["b3"]["state"]) != `"ready"` || strings.Contains(string(data["b3"]["rows"]), "Again") {
		t.Fatalf("a row without the field a button reads hides that button, not the whole table: %s", data["b3"])
	}
}

// TestAFormHasNoSave: a form does not keep its result in a collection, a command does; a form with save is refused.
func TestAFormHasNoSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.WebNotes, `"command": "score"}`, `"command": "score", "save": "notes"}`, 1)
	if m == harness.WebNotes {
		t.Fatal("fixture replacement did not apply")
	}
	e.Refused("deck.project_create", map[string]any{"name": "web", "manifest": m}, 422, "save")
}

// TestASliceOfTextCutsCharacters: a slice of a text in an expression counts characters, as len does, so a title in
// Cyrillic or with an emoji is not cut inside a letter.
func TestASliceOfTextCutsCharacters(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("ru", strings.Replace(harness.WebNotes, `"display": "title"`, `"display": "title[:3] + '|' + title[-2:] + '|' + string(len(title))"`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "ru", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "Привет мир 😀"})}, nil)
	var recs []struct{ Title string }
	e.Must("deck.records_list", map[string]any{"project": "ru", "collection": "notes"}, &recs)
	if len(recs) != 1 || recs[0].Title != "При| 😀|12" {
		t.Fatalf("a slice of a text is of its characters: %+v", recs)
	}
}

// TestTheFormatAModelReadsHasOnlyWhatAModuleUses: manifest_format leaves out the blocks and button fields only deck's
// screens use, so a model learns one way to do each thing; a manifest that has them still loads.
func TestTheFormatAModelReadsHasOnlyWhatAModuleUses(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var format map[string]any
	e.Must("deck.manifest_format", nil, &format)
	raw, _ := json.Marshal(format)
	for _, gone := range []string{`"appearance"`, `"verb"`, `"until"`, `"keep_open"`} {
		if strings.Contains(string(raw), gone) {
			t.Fatalf("the format a model reads has no %s", gone)
		}
	}
	for _, kept := range []string{`"board"`, `"custom"`, `"chat"`, `"options"`} {
		if !strings.Contains(string(raw), kept) {
			t.Fatalf("the format a model reads keeps %s", kept)
		}
	}
	e.Project("work", strings.Replace(harness.BoardManifest, `{"label": "Move"`, `{"verb": "open", "screen": "board"}, {"label": "Move"`, 1))
}

// TestARefusalOfAScreenSaysWhereByPointerAndWhatFits: a refusal names the block by the JSON Pointer a patch edits,
// and an unknown field lists the fields there are.
func TestARefusalOfAScreenSaysWhereByPointerAndWhatFits(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	bad := strings.Replace(harness.BoardManifest, `"item": {"title": "title"},`, `"item": {"title": "title"}, "filter": ["colour"],`, 1)
	code, out := e.Call("deck.project_create", map[string]any{"name": "work", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), `/screens/0/blocks/0`) || !strings.Contains(string(out), "status") || !strings.Contains(string(out), "title") {
		t.Fatalf("a refusal says where by pointer and lists the fields: %d %s", code, out)
	}
}

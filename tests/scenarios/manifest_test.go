package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
)

const fullManifest = `{
  "version": 7,
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
     "result": {"type": ["object", "null"]}, "requires": ["tools"], "timeout_minutes": 30,
     "executor": {"type": "agent", "prompt_file": "prompts/collect.md", "skills": ["reader"]}},
    {"name": "parse", "title": "Parse a source", "description": "Parses one source page with a script.", "class": "internal",
     "input": {"type": "object", "properties": {"source": {"type": "integer", "format": "record"}}, "required": ["source"]},
     "callers": ["agent", "owner"], "executor": {"type": "script", "script": "parse"}},
    {"name": "rate", "title": "Rate links", "description": "Scores new links.", "class": "internal",
     "input": {"type": "object", "properties": {"limit": {"type": "integer"}, "card": {"type": "integer", "format": "card"},
       "process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Score the new links 1-10."}},
    {"name": "share", "title": "Share a link", "description": "Posts a link to the owner's feed service.", "class": "external",
     "input": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]},
     "executor": {"type": "tool", "tool": "post_link"}}
  ],
  "scripts": [{"name": "parse", "file": "scripts/parse.py", "runtime": "python", "timeout_minutes": 5}],
  "triggers": [
    {"command": "collect", "cron": "0 8 * * *"},
    {"command": "rate", "after": "collect", "params": {"limit": 20}},
    {"command": "rate", "lane": "share"}
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
  "board": {"lanes": [{"name": "found", "title": "Found", "role": "inbox"}, {"name": "share", "title": "Share"}, {"name": "done", "title": "Done", "role": "done"}]},
  "screens": [{"name": "main", "title": "Links", "blocks": [{"type": "table", "title": "Best",
    "source": {"collection": "links", "where": "score >= 7", "sort": "-score", "limit": 20},
    "columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
    "actions": [{"label": "Share", "command": "share", "params": {"url": "$record.url"}}]}]}],
  "menu": ["main"],
  "autonomy": "suggest"
}`

func TestFullManifestIsAccepted(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	var schema map[string]any
	e.must("deck.manifest_format", nil, &schema)
	if schema["title"] != "deck project manifest" {
		t.Fatalf("manifest_format must return the schema, got %v", schema["title"])
	}
	if b, _ := json.Marshal(schema["$defs"].(map[string]any)["block"]); !strings.Contains(string(b), `"required":["source","columns"]`) ||
		!strings.Contains(string(b), "never both") || !strings.Contains(string(b), "Rows of a source.") {
		t.Fatalf("the model reads the form of each block of the catalog and its rules in words: %s", b)
	}
	var cmds []commandView
	code, out := e.do("GET", "/api/commands", nil)
	if code != 200 {
		t.Fatal(code)
	}
	_ = json.Unmarshal(out, &cmds)
	found := false
	for _, c := range cmds {
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
	e := newEnv(t, nil)
	e.createProject("links", strings.Replace(fullManifest, `{"label": "Share", "command": "share", "params": {"url": "$record.url"}}`,
		`{"label": "Share", "command": "share", "params": {"url": "$record.url"}}, {"label": "Parse", "command": "parse", "params": {"source": "7"}}`, 1))
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "u", "title": "t", "score": 8}`}, nil)
	rows := blockData[[]screenRow](t, e.screenData("links", "main", nil)["b0"]["rows"])
	if len(rows) != 1 || len(rows[0].Actions) != 2 {
		t.Fatalf("the row has both buttons: %+v", rows)
	}
	parse := rows[0].Actions[1]
	if parse["params"].(map[string]any)["source"] != float64(7) {
		t.Fatalf("a fixed param goes out as the integer of its field: %+v", parse)
	}
	if code, out := e.call("links.parse", parse["params"]); code == 422 && strings.Contains(string(out), "want integer") {
		t.Fatalf("the press of the button matches the input of its command: %s", out)
	}
}

func TestManifestReferencesAreChecked(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("other", ideasManifest)
	cases := []struct{ name, old, new, want string }{
		{"unknown field", `"menu": ["main"]`, `"menu": ["main"], "colour": "red"`, "colour"},
		{"trigger cycle", `{"command": "rate", "after": "collect", "params": {"limit": 20}}`,
			`{"command": "rate", "after": "collect", "params": {"limit": 20}}, {"command": "collect", "after": "rate"}`, "cycle: collect → rate → collect"},
		{"unknown lane", `"lane": "share"`, `"lane": "later"`, `unknown lane "later"`},
		{"trigger params", `"params": {"limit": 20}`, `"params": {"limit": "many"}`, "params do not match"},
		{"nested input", `"properties": {"limit": {"type": "integer"}, "card"`, `"properties": {"limit": {"type": "object"}, "card"`, "App Intents"},
		{"lane input", `"card": {"type": "integer", "format": "card"},`, ``, `must declare input field "card"`},
		{"unknown script", `"script": "parse"}`, `"script": "scrape"}`, `unknown script "scrape"`},
		{"unknown tool", `"tool": "post_link"`, `"tool": "post_it"`, `unknown tool "post_it"`},
		{"unknown external", `"allowed_external": ["post_link"]`, `"allowed_external": ["mail"]`, `"mail" is neither`},
		{"duplicate role", `"title": "Share"}`, `"title": "Share", "role": "inbox"}`, `role "inbox"`},
		{"duplicate lane", `{"name": "done", "title": "Done"`, `{"name": "share", "title": "Done"`, `duplicate lane "share"`},
		{"partial run roles", `"title": "Share"}`, `"title": "Share", "role": "ready"}`, `missing running, review`},
		{"lane title", `"title": "Share"}`, `"title": ""}`, `title`},
		{"key field", `"key": "url", "display": "title`, `"key": "link", "display": "title`, `unknown field "link"`},
		{"where field", `"where": "score >= 7"`, `"where": "rating >= 7"`, `unknown field "rating"`},
		{"sort field", `"sort": "-score"`, `"sort": "-rank"`, `unknown field "rank"`},
		{"action field", `"params": {"url": "$record.url"}`, `"params": {"url": "$record.href"}`, `unknown field "href"`},
		{"immutable field", `"immutable": ["found_at"]`, `"immutable": ["seen_at"]`, `unknown field "seen_at"`},
		{"menu", `"menu": ["main"]`, `"menu": ["home"]`, `unknown screen "home"`},
		{"unknown function", `"display": "title + ' (' + site + ')'"`, `"display": "shout(title)"`, `unknown function "shout"`},
		{"action params", `"params": {"url": "$record.url"}`, `"params": {"link": "$record.url"}`, `needs param "url"`},
		{"reads project", `"autonomy": "suggest"`, `"autonomy": "suggest", "reads": [{"project": "nope"}]`, `no project "nope"`},
		{"reads collection", `"autonomy": "suggest"`, `"autonomy": "suggest", "reads": [{"project": "other", "collections": ["nope"]}]`, `no collection "nope"`},
		{"trigger caller", `"callers": ["agent", "owner"], "executor": {"type": "script"`, `"callers": ["agent"], "executor": {"type": "script"`, ""},
	}
	for i, c := range cases {
		m := strings.Replace(fullManifest, c.old, c.new, 1)
		if m == fullManifest {
			t.Fatalf("%s: fixture replacement did not apply", c.name)
		}
		code, out := e.call("deck.project_create", map[string]any{"name": fmt.Sprintf("links%d", i), "manifest": m})
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
	m := strings.Replace(fullManifest, `"autonomy": "suggest"`, `"autonomy": "suggest", "reads": [{"project": "other", "collections": ["ideas"]}]`, 1)
	e.createProject("links", m)
}

func TestScriptCommandNeedsItsFileAndInput(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	if code, out := e.call("links.parse", map[string]any{"source": 1}); code != 422 || !strings.Contains(string(out), "scripts/parse.py") {
		t.Fatalf("a script command without its script file is refused: %d %s", code, out)
	}
	if code, out := e.call("links.parse", map[string]any{}); code != 422 || !strings.Contains(string(out), "source") {
		t.Fatalf("input schema must be enforced: %d %s", code, out)
	}
}

func TestPromptFileAndSkillsFeedTheAgent(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("links", fullManifest)
	if code, out := e.call("links.collect", nil); code != 422 || !strings.Contains(string(out), "prompts/collect.md") {
		t.Fatalf("missing prompt file must be refused, got %d %s", code, out)
	}
	e.must("deck.document_save", map[string]any{"project": "links", "name": "prompts/collect.md", "kind": "doc", "text": "Visit each source slowly."}, nil)
	if code, out := e.call("links.collect", nil); code != 422 || !strings.Contains(string(out), `skill "reader" is missing`) {
		t.Fatalf("missing skill must be refused, got %d %s", code, out)
	}
	skill := "---\nname: reader\ndescription: Reads pages.\n---\nRead slowly.\n"
	e.must("deck.document_save", map[string]any{"name": "skills/reader/SKILL.md", "kind": "skill", "text": skill}, nil)
	e.must("deck.document_save", map[string]any{"name": "skills/reader/notes/sites.md", "kind": "skill", "text": "example.com"}, nil)
	got := e.waitState(e.run(p, "collect"), "done")
	call := e.Fake.Calls()[0]
	if got.NotBySchema || !strings.Contains(call.Prompt, "Visit each source slowly.") {
		t.Fatalf("prompt file must reach the agent and null must pass the schema: %+v", got)
	}
	if call.Skills["reader/SKILL.md"] != skill || call.Skills["reader/notes/sites.md"] != "example.com" || len(call.Skills) != 2 {
		t.Fatalf("the files of the command's skills must reach the agent: %v", call.Skills)
	}
}

func TestSchemaChangesAreCheckedAgainstRecords(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	var lines []map[string]any
	for i := range 25 {
		lines = append(lines, map[string]any{"url": fmt.Sprintf("https://x.example/%d", i), "title": fmt.Sprintf("t%d", i), "found_at": "2026-09-25"})
	}
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": jsonl(lines...)}, nil)

	compatible := strings.Replace(fullManifest, `"found_at": {"type": "string"}}`, `"found_at": {"type": "string"}, "note": {"type": "string"}}`, 1)
	e.must("deck.manifest_save", map[string]any{"project": "links", "manifest": compatible}, nil)

	breaking := strings.Replace(compatible, `"required": ["url", "title"]`, `"required": ["url", "title", "note"]`, 1)
	code, out := e.call("deck.manifest_save", map[string]any{"project": "links", "manifest": breaking})
	msg := string(out)
	if code != 422 || !strings.Contains(msg, "25 of 25 records") || strings.Count(msg, "  - record") != 20 || !strings.Contains(msg, "migration script") {
		t.Fatalf("breaking change must be refused with the first 20 records and the total, got %d %s", code, msg)
	}

	rekey := strings.Replace(compatible, `"key": "url", "display": "title`, `"key": "title", "display": "title`, 1)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "links", "manifest": rekey}); code != 422 || !strings.Contains(string(out), "key would change") {
		t.Fatalf("key change must be refused, got %d %s", code, out)
	}
	sameKey := strings.Replace(compatible, `"key": "url", "display": "title`, `"key": "site", "display": "title`, 1)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "links", "manifest": sameKey}); code != 422 || !strings.Contains(string(out), "key") {
		t.Fatalf("key that breaks every record must be refused, got %d %s", code, out)
	}

	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "links"}, &cur)
	if cur.Manifest != compatible {
		t.Fatalf("refused revisions must leave the manifest unchanged")
	}

	removed := strings.Replace(strings.Replace(compatible, `"collection": "links"`, `"collection": "sources"`, 1), `"where": "score >= 7", "sort": "-score"`, `"sort": "url"`, 1)
	removed = strings.Replace(removed, `{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}`, `{"label": "URL", "bind": "url"}`, 1)
	removed = strings.Replace(removed, `"params": {"url": "$record.url"}`, `"params": {"url": "$record.url"}`, 1)
	start := strings.Index(removed, `    {"name": "links"`)
	end := strings.Index(removed, "\n  ],\n  \"commands\"")
	removed = removed[:start-2] + removed[end:]
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "links", "manifest": removed}); code != 422 || !strings.Contains(string(out), "has 25 records") {
		t.Fatalf("removing a collection with records must be refused, got %d %s", code, out)
	}
	var del struct{ Deleted int }
	e.must("deck.collection_delete", map[string]any{"project": "links", "collection": "links"}, &del)
	if del.Deleted != 25 {
		t.Fatalf("collection_delete: %+v", del)
	}
	e.must("deck.manifest_save", map[string]any{"project": "links", "manifest": removed}, nil)
}

func TestImmutableFieldsCannotChange(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	rec := map[string]any{"url": "https://x.example/1", "title": "one", "found_at": "2026-09-25"}
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": jsonl(rec)}, nil)

	rec["found_at"] = "2026-09-26"
	var up struct {
		Written int
		Errors  []string
	}
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": jsonl(rec)}, &up)
	if up.Written != 0 || len(up.Errors) != 1 || !strings.Contains(up.Errors[0], "found_at") {
		t.Fatalf("upsert must not change an immutable field: %+v", up)
	}
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	b, _ := json.Marshal(rec)
	if code, out := e.call("deck.record_update", map[string]any{"record": recs[0].ID, "data": string(b)}); code != 422 || !strings.Contains(string(out), "found_at") {
		t.Fatalf("record_update must not change an immutable field: %d %s", code, out)
	}
	rec["found_at"], rec["title"] = "2026-09-25", "one, edited"
	b, _ = json.Marshal(rec)
	e.must("deck.record_update", map[string]any{"record": recs[0].ID, "data": string(b)}, nil)
}

func TestKeyFieldsStayAndEmptyTriggersAreRefused(t *testing.T) {
	e := newEnv(t, nil)
	contacts := `{"version": 7,
	  "collections": [{"name": "contacts", "description": "People", "key": "lower(email)",
	    "schema": {"type": "object", "properties": {"email": {"type": "string"}, "big": {"type": "integer"}}, "required": ["email"]}}],
	  "commands": [{"name": "fetch-all", "title": "Fetch", "description": "Fetches <all> & more", "class": "internal",
	    "executor": {"type": "agent", "prompt": "Fetch."}}]}`
	e.createProject("contacts", contacts)
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "contacts"}, &cur)
	if !strings.Contains(cur.Manifest, "<all> & more") {
		t.Fatalf("a stored manifest keeps its text as is:\n%s", cur.Manifest)
	}
	e.must("deck.records_upsert", map[string]any{"project": "contacts", "collection": "contacts", "records": `{"email": "A@x.example"}`}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "contacts", "collection": "contacts"}, &recs)
	code, out := e.call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"email": "a@x.example"}`})
	if code != 422 || !strings.Contains(string(out), `"email"`) {
		t.Fatalf("a field of the key expression must stay immutable, got %d %s", code, out)
	}
	for _, bad := range []string{`"cron": ""`, `"lane": ""`} {
		m := strings.Replace(fullManifest, `"cron": "0 8 * * *"`, bad, 1)
		if code, _ := e.call("deck.project_create", map[string]any{"name": "empty", "manifest": m}); code != 422 {
			t.Errorf("trigger with %s must be refused, got %d", bad, code)
		}
	}
}

// TestAManifestOlderThanFormat4IsRefused: format 4 is still raised to the current one; older ones are refused.
func TestAManifestOlderThanFormat4IsRefused(t *testing.T) {
	e := newEnv(t, nil)
	v4 := strings.Replace(ideasManifest, `"version": 7,`, `"version": 4,`, 1)
	e.createProject("ideas", v4)
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if !strings.Contains(cur.Manifest, `"version": 7`) {
		t.Fatalf("a format 4 manifest is stored in the current format:\n%s", cur.Manifest)
	}
	v3 := strings.Replace(ideasManifest, `"version": 7,`, `"version": 3,`, 1)
	e.refused("deck.project_create", map[string]any{"name": "old", "manifest": v3}, 422, "version must be the number 7")
}

// TestOnlyTheBuiltInProjectAnswersEventsOfDeck: a trigger on a message or a failure belongs to project master.
func TestOnlyTheBuiltInProjectAnswersEventsOfDeck(t *testing.T) {
	e := newEnv(t, nil)
	m := strings.Replace(plainManifest, `"triggers": [],`, `"triggers": [{"command": "work", "on": "failure"}],`, 1)
	m = strings.Replace(m, `"executor": {"type": "agent", "prompt": "Do the work."}`,
		`"callers": ["trigger"], "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}}, "executor": {"type": "agent", "prompt": "Do the work."}`, 1)
	e.refused("deck.project_create", map[string]any{"name": "spy", "manifest": m}, 422, "belongs to the built-in project master")
}

func TestFormatFiveDropsWhatNothingDoes(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	old := `{"version": 4, "master_events": true, "allowed_external": ["deck.browser_click"], "domains": {"read": ["a.example"]},
  "commands": [{"name": "rate", "title": "Rate", "description": "Rates.", "class": "internal", "result": {"type": "object"},
    "requires": ["live_input", "structured_output", "no_cloud"], "executor": {"type": "agent", "prompt": "Rate."}},
   {"name": "sum", "title": "Sum", "description": "Sums.", "class": "internal", "result": {"type": "object"},
    "requires": ["structured_output"], "executor": {"type": "agent", "prompt": "Sum."}}]}`
	e.createProject("old", old)
	var got struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "old"}, &got)
	if !strings.Contains(got.Manifest, `"version": 7`) || strings.Contains(got.Manifest, "master_events") ||
		strings.Contains(got.Manifest, "live_input") || strings.Contains(got.Manifest, "browser_click") || strings.Contains(got.Manifest, "structured_output") || !strings.Contains(got.Manifest, `"no_cloud"`) {
		t.Fatalf("format 4 comes up to 5 without the fields nothing reads:\n%s", got.Manifest)
	}
	code, out := e.call("deck.manifest_save", map[string]any{"project": "old", "manifest": strings.Replace(old, `"version": 4`, `"version": 7`, 1)})
	if code == 200 || !strings.Contains(string(out), "master_events") {
		t.Fatalf("format 5 has no master_events: %d %s", code, out)
	}
}

func TestProjectPromptReachesTheAgent(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	var p struct{ ID int64 }
	e.must("deck.project_create", map[string]any{"name": "ideas", "manifest": ideasManifest, "prompt": "Ideas for a solo developer.\nPrefer Go."}, &p)
	e.names[p.ID] = "ideas"
	e.waitState(e.run(p.ID, "collect"), "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "Prefer Go.") {
		t.Fatalf("project prompt must be part of the agent prompt, got %+v", calls)
	}
}

func TestExpressionVariablesCannotHideFields(t *testing.T) {
	e := newEnv(t, idle)
	key := strings.Replace(calcManifest, `"key": "n"`, `"key": "string(n) + (let n = 1; string(n))"`, 1)
	e.refused("deck.project_create", map[string]any{"name": "a", "manifest": key}, 422, "not allowed in a key")
	display := strings.Replace(calcManifest, `"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`, 1)
	e.refused("deck.project_create", map[string]any{"name": "b", "manifest": display}, 422, `hides the field`)
}

func TestVariablesNeedKnownFields(t *testing.T) {
	e := newEnv(t, idle)
	m := strings.NewReplacer(`"schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}`, `"schema": {"type": "object"}`,
		`"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`).Replace(calcManifest)
	e.refused("deck.project_create", map[string]any{"name": "a", "manifest": m}, 422, "need a schema that lists its properties")
}

// TestAFormatFiveManifestKeepsItsParamsAndMenus: a manifest of format 5 still loads; its $param values are read
// as declared params, and the menu of a group of buttons is dropped.
func TestAFormatFiveManifestKeepsItsParamsAndMenus(t *testing.T) {
	e := newEnv(t, nil)
	old := strings.Replace(strings.Replace(boardWebManifest, `"version": 7,`, `"version": 5,`, 1), `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "form", "title": "Again", "command": "score", "values": {"title": "$param.title"}},
      {"type": "actions", "title": "More", "menu": true, "actions": [{"label": "Tell", "command": "tell"}]},`, 1)
	e.createProject("web", old)
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "web"}, &cur)
	if !strings.Contains(cur.Manifest, `"=param.title"`) || !strings.Contains(cur.Manifest, `"title": "string"`) || strings.Contains(cur.Manifest, `"menu": true`) {
		t.Fatalf("format 5 is raised to the current one:\n%s", cur.Manifest)
	}
	data := e.screenData("web", "main", map[string]any{"params": `{"title": "alpha"}`})
	if !strings.Contains(string(data["b0"]["value"]), `"values":{"title":"alpha"}`) {
		t.Fatalf("the declared param fills the form: %s", data["b0"]["value"])
	}
}

// TestAVersionThatIsNoNumberSaysSo: a version written as text or a fraction is refused with what it is.
func TestAVersionThatIsNoNumberSaysSo(t *testing.T) {
	e := newEnv(t, nil)
	for i, c := range []struct{ version, want string }{{`"6"`, `the text "6"`}, {`6.0`, `6.0`}} {
		m := strings.Replace(boardWebManifest, `"version": 7`, `"version": `+c.version, 1)
		code, out := e.call("deck.project_create", map[string]any{"name": fmt.Sprintf("v%d", i), "manifest": m})
		if code != 422 || !strings.Contains(string(out), c.want) || !strings.Contains(string(out), "the number 7") {
			t.Errorf("%s: want 422 naming %s, got %d %s", c.version, c.want, code, out)
		}
	}
}

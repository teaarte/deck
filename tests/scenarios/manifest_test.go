package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

const fullManifest = `{
  "version": 5,
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

const ideasV1 = `{
  "version": 1,
  "description": "Ideas of the day",
  "collections": [{"name": "ideas", "description": "Short ideas", "key": "title", "title": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [{"name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "Write ideas."}, "result_schema": {"type": "object"}}]
}`

func TestOldManifestFormatIsUpgraded(t *testing.T) {
	e := newEnv(t, nil)
	p := e.createProject("ideas", ideasV1)
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if !strings.Contains(cur.Manifest, `"version": 5`) || !strings.Contains(cur.Manifest, `"display": "title"`) || strings.Contains(cur.Manifest, "result_schema") {
		t.Fatalf("saved v1 manifest must be stored upgraded:\n%s", cur.Manifest)
	}

	ctx := context.Background()
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, _, err := app.SaveDocument(ctx, q, app.DocRef{Scope: app.Scope{Workspace: 1, Project: p}, Name: app.ManifestName}, app.KindManifest, ideasV1, "owner", "stored by an old deck")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.App.UpgradeManifests(ctx); err != nil {
		t.Fatal(err)
	}
	var revs []struct {
		Revision int64
		Author   string
		Reason   string
	}
	var doc struct{ Document int64 }
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &doc)
	e.must("deck.document_revisions", map[string]any{"document": doc.Document}, &revs)
	if len(revs) != 3 || revs[0].Author != "deck" || !strings.Contains(revs[0].Reason, "version 5") {
		t.Fatalf("startup upgrade must add a deck revision, got %+v", revs)
	}
	if err := e.App.UpgradeManifests(ctx); err != nil {
		t.Fatal(err)
	}
	e.must("deck.document_revisions", map[string]any{"document": doc.Document}, &revs)
	if len(revs) != 3 {
		t.Fatalf("a current manifest must not be upgraded again, got %d revisions", len(revs))
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

func TestLooseV1ManifestUpgrades(t *testing.T) {
	e := newEnv(t, nil)
	loose := `{"version": 1, "description": "", "triggers": null, "screens": null,
	  "collections": [{"name": "contacts", "description": "People", "key": "lower(email)", "title": "",
	    "schema": {"type": "object", "properties": {"email": {"type": "string"}, "big": {"type": "integer"}}, "required": ["email"]}}],
	  "commands": [{"name": "fetch-all", "title": "Fetch", "description": "Fetches <all> & more", "class": "internal", "result_schema": null,
	    "executor": {"type": "agent", "prompt": "Fetch."}}]}`
	e.createProject("contacts", loose)
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "contacts"}, &cur)
	if strings.Contains(cur.Manifest, "null") || !strings.Contains(cur.Manifest, "<all> & more") {
		t.Fatalf("upgrade must drop empty values and keep text as is:\n%s", cur.Manifest)
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

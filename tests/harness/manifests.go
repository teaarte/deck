package harness

import (
	"deck2/internal/manifest"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var NoNetworkManifest = Manifest(`{"no_network": true,
  "commands": [{"name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}}]}`)

func BrowserManifest(domains string) string {
	return Manifest(`{"commands": [{"name": "browse", "title": "Browse", "description": "Browses.", "class": "internal",
    "input": {"type": "object", "properties": {"steps": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Browse."}}],
  "domains": `) + domains + `}`
}

// EstimateManifest is a module that tracks a price and estimates its change: a stat of the last price, a table
// of estimates and a estimate page, each value shown by its format.
var EstimateManifest = Manifest(`{"collections": [
    {"name": "prices", "description": "Prices", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "price": {"type": "number"}, "estimate": {"type": "number"}}, "required": ["at"]}},
    {"name": "estimates", "description": "Estimates", "key": "at", "display": "at",
      "schema": {"type": "object", "properties": {"at": {"type": "string"}, "horizon": {"type": "string"}, "change": {"type": "number"},
        "confidence": {"type": "number"}, "status": {"type": "string"}, "source": {"type": "string"}}, "required": ["at"]}},
    {"name": "attachments", "description": "Files", "key": "title", "display": "title",
      "schema": {"type": "object", "properties": {"title": {"type": "string"}, "file": {}}, "required": ["title"]}}],
  "screens": [
    {"name": "main", "title": "COIN", "blocks": [
      {"type": "stat", "title": "Last price", "source": {"collection": "prices", "sort": "-at", "limit": 1}, "value": "rows[0].price",
        "format": "money", "currency": "USD", "tone": "rows[0].price >= 60000 ? 'ok' : 'warn'", "empty": "No prices yet"},
      {"type": "table", "title": "Estimates", "source": {"collection": "estimates", "sort": "-at"},
        "columns": [{"label": "Made", "bind": "at", "format": "relative"}, {"label": "Horizon", "bind": "horizon"},
          {"label": "Change", "bind": "change", "format": "percent", "tone": "change < 0 ? 'error' : 'ok'"},
          {"label": "Status", "bind": "status", "format": "tag", "tone": "status == 'missed' ? 'warn' : 'none'"},
          {"label": "Source", "bind": "source", "format": "link"}],
        "actions": [{"label": "Open", "screen": "estimate", "primary": true}], "empty": "No estimates yet"},
      {"type": "list", "title": "Horizons", "source": {"collection": "estimates", "sort": "-at"},
        "item": {"title": "horizon", "tone": "status == 'missed' ? 'muted' : ''"}},
      {"type": "list", "title": "Missed", "when": "len(rows) > 0", "source": {"collection": "estimates", "where": "status == 'missed'"},
        "item": {"title": "horizon"}},
      {"type": "chart", "title": "Price", "source": {"collection": "prices", "sort": "at"}, "x": "at",
        "series": [{"label": "COIN", "y": "price"}, {"label": "Estimate", "y": "estimate"}], "currency": "USD", "format": "money", "empty": "No prices yet"}]},
    {"name": "files", "title": "Files", "blocks": [
      {"type": "media", "title": "Attachments", "source": {"collection": "attachments", "sort": "title"}, "item": {"file": "file", "title": "title"},
        "empty": "No files yet"}]},
    {"name": "projects", "title": "Projects", "blocks": [
      {"type": "chart", "kind": "bar", "title": "Revisions", "source": {"from": "deck.projects_list"}, "x": "name", "series": [{"label": "Revision", "y": "revision"}]}]},
    {"name": "estimate", "title": "Estimate", "record": "estimates", "blocks": [
      {"type": "card", "fields": [{"label": "Made", "bind": "at", "format": "datetime"}, {"label": "Change", "bind": "change", "format": "percent", "tone": "change < 0 ? 'error' : 'ok'"},
        {"label": "Confidence", "bind": "confidence", "format": "percent"}]},
      {"type": "card", "title": "Still open", "when": "status == 'open'", "fields": [{"label": "Horizon", "bind": "horizon"}]}]}],
  "menu": ["main"]}`)

// TasksManifest is a project that keeps its tasks in a collection, as the master puts them there.
var TasksManifest = Manifest(`{"collections": [{"name": "tasks", "description": "A task of the project: what to do.", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "text": {"type": "string"}}, "required": ["title"]}}],
  "screens": [{"name": "tasks", "title": "Tasks", "blocks": [{"type": "table", "title": "Tasks", "source": {"collection": "tasks"},
    "columns": [{"label": "Task", "bind": "title"}]}]}],
  "menu": ["tasks"]}`)

var PlainManifest = Manifest(`{
  "collections": [],
  "commands": [{
    "name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}
  }],
  "triggers": [],
  "screens": []
}`)

// AgentCommand is a command of a test manifest; input and after are optional.
func AgentCommand(name, input string) string {
	if input == "" {
		input = `{"type": "object", "properties": {}}`
	}
	return fmt.Sprintf(`{"name": %q, "title": %q, "description": "Does %s.", "class": "internal", "input": %s,
    "executor": {"type": "agent", "prompt": "Do %s."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}}`,
		name, name, name, input, name)
}

const ProcessField = `{"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}, "required": ["process"]}`

// Nullable lets the results of the agent commands of a manifest be null, nothing to do.
func Nullable(m string) string {
	return strings.ReplaceAll(m, `"result": {"type": "object"`, `"result": {"type": ["object", "null"]`)
}

func TestManifest(commands []string, triggers []string) string {
	return Manifest(fmt.Sprintf(`{"commands": [%s], "triggers": [%s]}`, strings.Join(commands, ","), strings.Join(triggers, ",")))
}

// CodeModule is a copy of the example of the code-project skill; with a url it also takes GitLab issues from there, the way a terminal
// agent adds a tracker by the code-project skill (testdata/tracker).
func CodeModule(t *testing.T, url string) string {
	t.Helper()
	src := filepath.Join(Root, "skills/code-project/example")
	dst := filepath.Join(t.TempDir(), "code")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "" {
		addTracker(t, dst, url)
	}
	return dst
}

var ColorManifest = Manifest(`{"commands": [{"name": "work", "title": "Work", "description": "Asks the owner", "class": "internal",
    "result": {"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Ask the owner which color the report should use."}}]}`)

var IdeasManifest = Manifest(`{
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "Write three ideas into the ideas collection."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [{"command": "collect", "cron": "0 9 * * *"}],
  "screens": [{
    "name": "main", "title": "Ideas",
    "blocks": [{"type": "table", "title": "Best ideas",
      "source": {"collection": "ideas", "where": "score >= 5", "sort": "-score", "limit": 10},
      "columns": [{"label": "Idea", "bind": "title"}, {"label": "Score", "bind": "score"}],
      "actions": [{"label": "Again", "command": "collect"}]}]
  }]
}`)

var CalcManifest = Manifest(`{
  "collections": [{"name": "nums", "description": "A number", "key": "n",
    "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}],
  "commands": [{"name": "calc", "title": "Calc", "description": "Uses a number.", "class": "internal",
    "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "OLD PROMPT"}}]
}`)

// WidgetsManifest is a module with a view of one cell and one of two by two.
var WidgetsManifest = Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name", "display": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "n": {"type": "integer"}}, "required": ["name"]}}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Items", "source": {"collection": "items"}}]},
    {"min": "2x2", "blocks": [{"type": "list", "title": "Newest", "source": {"collection": "items", "sort": "-n"}, "item": {"title": "name"}}]}
  ]}`)

// DocsManifest is the web project with a document block on its main screen and one of the record on its page,
// a list and a card with a field in markdown.
var DocsManifest = strings.NewReplacer(
	`{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},`, `{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},
      {"type": "document", "title": "Criteria", "document": "notes/criteria.md"},
      {"type": "list", "title": "Posts", "source": {"collection": "notes"}, "item": {"title": "title", "text": "note"}},`,
	`{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`, `{"type": "form", "title": "Edit", "command": "deck.record_update"},
      {"type": "card", "fields": [{"label": "Note", "bind": "note", "markdown": true}, {"label": "Title", "bind": "title"}]},
      {"type": "document", "title": "Report", "document": "report.md"}]}`,
).Replace(WebNotes)

var DraftManifest = Manifest(`{
  "commands": [
    {"name": "work", "title": "Work", "description": "Does work.", "class": "internal", "input": {"type": "object", "properties": {}},
     "executor": {"type": "agent", "prompt": "Do the work."}, "result": {"type": "object"}},
    {"name": "child", "title": "Child", "description": "Helps.", "class": "internal", "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Help."}, "result": {"type": "object"}},
    {"name": "send", "title": "Send", "description": "Sends it out.", "class": "external",
     "executor": {"type": "agent", "prompt": "Send."}, "result": {"type": "object"}}
  ],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}, {"command": "child", "after": "work"}],
  "domains": {"read": ["example.com"]}
}`)

func FilesManifest() string {
	return Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "seed", "title": "Seed", "description": "Makes a record with a file.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "seed"}},
    {"name": "read", "title": "Read", "description": "Reads the files of its input.", "class": "internal",
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}, "photo": {"type": "string", "format": "file"},
       "rec": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "read"}}],
  "scripts": [{"name": "seed", "file": "scripts/seed.py", "runtime": "python"},
    {"name": "read", "file": "scripts/read.py", "runtime": "python"}]}`)
}

// FolderCommand is an agent command of a test manifest that works in a project folder.
func FolderCommand(name, folder string) string {
	return strings.Replace(AgentCommand(name, ""), `"executor": {"type": "agent",`, `"executor": {"type": "agent", "folder": `+folder+`,`, 1)
}

func GenerateManifest(url string) string {
	return Manifest(fmt.Sprintf(`{"commands": [
    {"name": "make", "title": "Make", "description": "Starts a paid generation.", "class": "external",
     "input": {"type": "object", "properties": {"topic": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "generate"}},
    {"name": "work", "title": "Work", "description": "Asks for a generation.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Generate."}}],
  "triggers": [{"command": "make", "cron": "0 9 * * *", "params": {"topic": "cats"}}],
  "tools": {"http": [{"name": "generate", "description": "Starts a generation", "method": "POST", "url": "%s/gen",
    "input": {"type": "object", "properties": {"topic": {"type": "string"}}}}]},
  "allowed_external": ["generate", "make"]}`, url))
}

// McpManifest declares the fake MCP server; extra goes into the manifest object.
func McpManifest(extra string) string {
	self := Fakes
	return Manifest(fmt.Sprintf(`{"commands": [
    {"name": "work", "title": "Work", "description": "Works with the server.", "class": "internal",
     "input": {"type": "object", "properties": {"step": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}},
    {"name": "look", "title": "Look up", "description": "Looks a word up.", "class": "internal",
     "input": {"type": "object", "properties": {"word": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "fake.lookup"}},
    {"name": "tell", "title": "Tell", "description": "Sends a message.", "class": "internal",
     "input": {"type": "object", "properties": {"to": {"type": "string"}}, "required": ["to"]},
     "executor": {"type": "tool", "tool": "fake.send"}},
    {"name": "reach", "title": "Reach", "description": "Reaches an address.", "class": "internal",
     "input": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]},
     "executor": {"type": "tool", "tool": "fake.reach"}}],
  "tools": {"mcp": {"servers": {"fake": {"command": %q, "env": {"DECK_FAKE_MCP": "project"},
    "toolFilter": ["lookup", "send", "picture", "vanish", "peek", "reach"], "classes": {"lookup": "internal", "picture": "internal", "peek": "internal", "reach": "internal"}}}}}%s}`, self, extra))
}

// VideosManifest is a module of short videos: a media block plays each video in the browser's own player.
var VideosManifest = Manifest(`{"collections": [{"name": "videos", "description": "Videos", "key": "slug", "display": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "file": {"type": "string"}, "length": {"type": "number"}}, "required": ["slug"]}}],
  "screens": [{"name": "watch", "title": "Watch", "blocks": [
    {"type": "media", "title": "Videos", "span": "full", "source": {"collection": "videos", "sort": "slug"},
      "item": {"file": "file", "title": "slug"}},
    {"type": "table", "source": {"collection": "videos"}, "columns": [{"label": "Length", "bind": "length", "format": "duration"}]},
    {"type": "list", "source": {"collection": "videos"}, "item": {"title": "slug"}},
    {"type": "chart", "source": {"collection": "videos"}, "x": "slug", "series": [{"label": "Length", "y": "length"}]},
    {"type": "actions", "actions": [{"label": "Again", "screen": "watch"}]}]}],
  "menu": ["watch"]}`)

// ModelManifest is a project with commands of type model: a fact check by a schema, a picture, speech and the
// words of a recording.
var ModelManifest = Manifest(`{"commands": [
    {"name": "factcheck", "title": "Fact check", "description": "Checks a claim.", "class": "internal",
     "input": {"type": "object", "properties": {"claim": {"type": "string"}}, "required": ["claim"]},
     "result": {"type": "object", "properties": {"verdict": {"enum": ["true", "false", "unclear"]}, "confidence": {"type": "number"}},
       "required": ["verdict", "confidence"], "additionalProperties": false},
     "executor": {"type": "model", "kind": "text", "prompt": "Check the claim against what is known."}},
    {"name": "picture", "title": "Picture", "description": "Draws a scene.", "class": "internal",
     "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
     "executor": {"type": "model", "kind": "image", "prompt": "Draw the scene, no text in the picture.", "options": {"image_config": {"aspect_ratio": "9:16"}}}},
    {"name": "voice", "title": "Voice", "description": "Speaks a line.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]},
     "executor": {"type": "model", "kind": "tts", "prompt": "Calm, like a documentary.", "options": {"voice": "onyx", "model": "an-ungranted-model"}}},
    {"name": "words", "title": "Words", "description": "Writes down a recording.", "class": "internal",
     "input": {"type": "object", "properties": {"voice": {"type": "string", "format": "file"}}, "required": ["voice"]},
     "executor": {"type": "model", "kind": "stt", "options": {"timestamp_granularities[]": "word"}}},
    {"name": "track", "title": "Track", "description": "Makes background music.", "class": "internal",
     "input": {"type": "object", "properties": {"mood": {"type": "string"}}},
     "executor": {"type": "model", "kind": "music", "prompt": "Instrumental background music, no vocals.", "options": {"seconds": 40}}}
  ]}`)

// PickerManifest has proposals of models with an Apply button whose press allows the assignment its row shows, and
// a daily run of apply over all of them.
var PickerManifest = Manifest(`{"collections": [{"name": "proposals", "description": "A proposal of models.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "project": {"type": "string"}, "models": {"type": "string"},
      "command": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "apply", "title": "Apply", "description": "Applies a proposal.", "class": "internal",
     "input": {"type": "object", "properties": {"id": {"type": "integer"}, "relay": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "apply"}}],
  "scripts": [{"name": "apply", "file": "scripts/apply.py", "runtime": "python"}],
  "triggers": [{"command": "apply", "cron": "0 9 * * *"}],
  "screens": [{"name": "main", "title": "Proposals", "blocks": [{"type": "table", "title": "Proposals", "source": {"collection": "proposals"},
    "columns": [{"label": "Models", "bind": "models"}],
    "actions": [{"label": "Apply", "command": "apply", "params": {"id": "=_id"},
      "allows": [{"command": "deck.assignment_set", "params": {"role": "text", "project": "=project", "command": "=command ?? ''", "models": "=split(models, ',')"}}]}]}]}],
  "menu": ["main"]}`)

// BoardManifest is a module whose tasks move across the lanes of their status.
var BoardManifest = Manifest(`{"i18n": {"ru": {"doing": "в работе"}},
  "collections": [{"name": "tasks", "description": "Tasks.", "key": "title", "schema": {"type": "object",
    "properties": {"title": {"type": "string"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}],
  "commands": [{"name": "move", "title": "Move", "description": "Moves a task.", "class": "internal",
    "input": {"type": "object", "properties": {"id": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}},
    "executor": {"type": "agent", "prompt": "Move it."}}],
  "screens": [{"name": "board", "title": "Board", "blocks": [
    {"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks"}, "item": {"title": "title"},
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}]}],
  "menu": ["board"]}`)

var NotesManifest = Manifest(`{"collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "https://example.invalid/post",
    "class": "external", "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]},
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Collect notes."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`)

// RoutedManifest has a dialog with a form its address fills, and rows whose button opens it with their title.
var RoutedManifest = strings.Replace(WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"title": "string"}, "blocks": [
      {"type": "dialog", "name": "rescore", "title": "Score again", "icon": "refresh", "primary": true, "blocks": [
        {"type": "form", "title": "Score", "command": "score", "values": {"title": "=param.title", "n": "3"}}]},
      {"type": "actions", "title": "More", "icon": "dots", "actions": [{"label": "Tell", "command": "tell"}]},
      {"type": "table", "title": "Again", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
        "actions": [{"label": "Again", "screen": "main", "params": {"title": "=title", "dialog": "rescore"}}]},`, 1)

// WebNotes is a project with a list, a form that can keep its result, a custom block and a record
// page whose form edits the record.
var WebNotes = Manifest(`{"collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title", "immutable": ["source"],
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "source": {"type": "string"},
      "note": {"type": "string"}}, "required": ["title"]}}],
  "commands": [
    {"name": "score", "title": "Score", "description": "Scores a title.", "class": "internal",
      "input": {"type": "object", "properties": {"title": {"type": "string"}, "n": {"type": "integer"}, "scan": {"type": "string", "format": "file"}},
        "required": ["title"]},
      "result": {"type": "array", "items": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}}},
      "executor": {"type": "agent", "prompt": "Score it."}},
    {"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}}},
      "executor": {"type": "agent", "prompt": "Touch."}},
    {"name": "tell", "title": "Tell", "description": "Tells the owner.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Tell."}},
    {"name": "ingest", "title": "Ingest", "description": "Reads a file for agents.", "class": "internal", "callers": ["agent"],
      "result": {"type": "object"}, "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}},
      "executor": {"type": "agent", "prompt": "Ingest."}},
    {"name": "tag", "title": "Tag", "description": "Tags what a custom block saw.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"tag": {"type": "string"}}}, "executor": {"type": "agent", "prompt": "Tag."}},
    {"name": "publish", "title": "Publish", "description": "Publishes outside deck.", "class": "external", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Publish."}},
    {"name": "mark", "title": "Mark", "description": "Marks what a custom block saw.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"text": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Mark."}}],
  "screens": [
    {"name": "main", "title": "Web", "blocks": [
      {"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},
      {"type": "form", "title": "Score a title", "command": "score"},
      {"type": "custom", "title": "Chart", "document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},
        "actions": [{"label": "Mark", "command": "mark"}, {"label": "Tag a", "command": "tag", "params": {"tag": "a"}},
          {"label": "Tag b", "command": "tag", "params": {"tag": "b"}}, {"label": "Publish", "command": "publish"}]},
      {"type": "table", "title": "Notes", "source": {"collection": "notes"}, "filter": ["source"],
        "columns": [{"label": "Title", "bind": "title"}], "actions": [{"label": "Open", "screen": "note"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "form", "title": "Edit", "command": "deck.record_update"}]}],
  "menu": ["main"]}`)

// WebNotesMark is webNotes whose custom block fills the text of Mark itself.
var WebNotesMark = strings.Replace(WebNotes, `{"label": "Mark", "command": "mark"}`, `{"label": "Mark", "command": "mark", "from_block": ["text"]}`, 1)

// WebManifest is a project with every block of the catalog, a record page and an external tool.
func WebManifest(url string) string {
	return Manifest(fmt.Sprintf(`{"collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [
    {"name": "work", "title": "Work", "description": "Posts a note.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Post the note."}},
    {"name": "ask", "title": "Ask", "description": "Asks the owner.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Ask."}},
    {"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}, "score": {"type": "string"}}, "required": ["note"]},
      "executor": {"type": "agent", "prompt": "Touch."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "body": {"text": "{text}"},
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}}]},
  "screens": [
    {"name": "main", "title": "Notes", "blocks": [
      {"type": "columns", "blocks": [
        {"type": "stat", "title": "Notes", "source": {"collection": "notes"}},
        {"type": "stat", "title": "Score", "source": {"collection": "notes", "where": "score > 1"}, "value": "sum(rows, .score)"}]},
      {"type": "tabs", "title": "Views", "tabs": [
        {"title": "All", "blocks": [{"type": "table", "title": "All notes", "source": {"collection": "notes", "sort": "-score"},
          "columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
          "actions": [{"label": "Open", "screen": "note"}, {"label": "Touch", "command": "touch", "params": {"note": "=_id"}}]}]},
        {"title": "Top", "blocks": [{"type": "list", "source": {"collection": "notes", "where": "score >= 5"},
          "item": {"title": "title", "meta": "'score ' + string(score)"}}]}]},
      {"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}},
      {"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "card", "fields": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
        "actions": [{"label": "Touch", "command": "touch", "params": {"note": "=_id"}}]},
      {"type": "list", "title": "Better", "source": {"collection": "notes", "where": "score > record.score"}, "item": {"title": "title"}},
      {"type": "actions", "title": "Rate", "actions": [{"label": "Rate", "command": "touch", "params": {"note": "=_id", "score": "=score"}}]}]}],
  "menu": ["main"]}`, url))
}

// ScriptManifest is a manifest with one python script command; extra goes into the manifest object.
func ScriptManifest(command, result, extra string) string {
	if result == "" {
		result = `{"type": "object"}`
	}
	return Manifest(fmt.Sprintf(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}}],
  "commands": [
    {"name": %q, "title": "Run", "description": "Runs the script.", "class": "internal",
     "input": {"type": "object", "properties": {"n": {"type": "integer"}, "project": {"type": "string"}, "allowed": {"type": "string"},
       "other": {"type": "string"}, "outside": {"type": "string"}, "secret": {"type": "string"}, "key": {"type": "string"},
       "socket": {"type": "string"}, "method": {"type": "string"}, "form": {"type": "string"}, "type": {"type": "string"}, "use": {"type": "string"}}},
     "result": %s, "executor": {"type": "script", "script": "main"}},
    {"name": "publish", "title": "Publish", "description": "Publishes outside.", "class": "external",
     "executor": {"type": "agent", "prompt": "Publish."}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}]%s}`, command, result, extra))
}

var SideManifest = Manifest(`{"i18n": {"ru": {"Trades": "Сделки", "Coin": "Монета", "Side": "Сторона", "long": "в рост", "short": "в падение"}},
  "collections": [{"name": "trades", "description": "Trades", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "coin": {"type": "string"}, "side": {"type": "string"}}, "required": ["title"]}}],
  "commands": [],
  "screens": [{"name": "lab", "title": "Trades", "params": {"coin": "string", "side": "string"},
    "side": [
      {"type": "filter", "title": "Coin", "param": "coin", "source": {"collection": "trades"}, "field": "coin"},
      {"type": "filter", "title": "Side", "param": "side", "options": ["long", "short"]},
      {"type": "stat", "title": "Open", "source": {"collection": "trades", "where": "param.coin == nil || coin == param.coin"}},
      {"type": "actions", "actions": [{"label": "All coins", "screen": "lab"}]}],
    "blocks": [{"type": "table", "title": "Trades", "source": {"collection": "trades", "sort": "title",
        "where": "(param.coin == nil || coin == param.coin) && (param.side == nil || side == param.side)"},
      "columns": [{"label": "Title", "bind": "title"}]}]}]}`)

// ClipsModule keeps clips: new makes one, check follows it, tally follows check with no record, revise edits a clip,
// render (a script) follows revise.
var ClipsModule = Manifest(`{"collections": [{"name": "clips", "description": "A clip.", "key": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [
    {"name": "new", "title": "New", "description": "Makes a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Make a clip."}},
    {"name": "check", "title": "Check", "description": "Checks a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}, "record": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Check the clip."}},
    {"name": "tally", "title": "Tally", "description": "Counts checks.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}},
    {"name": "revise", "title": "Revise", "description": "Edits a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Edit the clip."}},
    {"name": "render", "title": "Render", "description": "Renders a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "process": {"type": "integer", "format": "process"},
       "n": {"type": "integer"}, "wait": {"type": "number"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}],
  "triggers": [{"after": "new", "command": "check"}, {"after": "check", "command": "tally"}, {"after": "revise", "command": "render"}]}`)

func ToolManifest(url, extra string) string {
	return Manifest(fmt.Sprintf(`{"commands": [
    {"name": "fetch", "title": "Fetch", "description": "Fetches a picture.", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "picture"}},
    {"name": "send", "title": "Send", "description": "Sends a note.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}},
     "result": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]},
     "executor": {"type": "tool", "tool": "note"}},
    {"name": "merge", "title": "Merge", "description": "Merges the work.", "class": "reversible",
     "executor": {"type": "agent", "prompt": "Merge."}},
    {"name": "work", "title": "Work", "description": "Works.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}}],
  "tools": {"http": [
    {"name": "picture", "description": "A picture", "method": "GET", "url": "%[1]s/pics/{name}?size={size}", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}}},
    {"name": "note", "description": "Sends a note", "method": "POST", "url": "%[1]s/notes",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}}}]}%[2]s}`, url, extra))
}

// Manifest is the manifest of a module in the current format: body is its JSON object without the version, which
// goes first, laid out as the next key is.
func Manifest(body string) string {
	i := strings.Index(body, "{")
	if i < 0 {
		panic("a manifest is a JSON object: " + body)
	}
	rest := body[i+1:]
	ws := rest[:len(rest)-len(strings.TrimLeft(rest, " \t\n"))]
	version := fmt.Sprintf(`"version": %d`, manifest.FormatVersion)
	switch {
	case strings.HasPrefix(strings.TrimLeft(rest, " \t\n"), "}"):
		return body[:i+1] + version + rest
	case strings.Contains(ws, "\n"):
		return body[:i+1] + ws + version + "," + rest
	default:
		return body[:i+1] + version + ", " + rest
	}
}

// ManifestWith is a manifest with more members right after its version.
func ManifestWith(m, members string) string {
	v := fmt.Sprintf(`"version": %d`, manifest.FormatVersion)
	if !strings.Contains(m, v) {
		panic("a manifest of the current format: " + m)
	}
	return strings.Replace(m, v, v+", "+members, 1)
}

// ManifestVersion is a manifest with its version written as v, as an older or a broken one.
func ManifestVersion(m, v string) string {
	return strings.Replace(m, fmt.Sprintf(`"version": %d`, manifest.FormatVersion), `"version": `+v, 1)
}

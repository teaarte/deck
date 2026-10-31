package modules

import (
	"context"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rightsManifest is a manifest of a random pick of every right deck grants, the same picks giving the same manifest.
func rightsManifest(r *rand.Rand) string {
	pick := func(of ...string) []string {
		var out []string
		for _, s := range of {
			if r.IntN(2) == 0 {
				out = append(out, s)
			}
		}
		return out
	}
	one := func(of ...string) string { return of[r.IntN(len(of))] }
	list := func(xs []string) string {
		b, _ := json.Marshal(xs)
		if xs == nil {
			return "[]"
		}
		return string(b)
	}
	var tools, names []string
	for _, name := range pick("page", "other") {
		names = append(names, name)
		t := fmt.Sprintf(`{"name": %q, "description": "Reads.", "method": %q, "url": %q, "class": %q`, name,
			one("GET", "POST"), one("https://a.example/x", "https://b.example/y"), one("internal", "reversible", "external"))
		if r.IntN(3) == 0 {
			t += `, "headers": {"Authorization": "Bearer $secret.` + one("k1", "k2") + `"}`
		}
		tools = append(tools, t+"}")
	}
	var cmds []string
	for _, name := range pick("alpha", "beta", "gamma") {
		names = append(names, name)
		execs := []string{`{"type": "agent", "prompt": "Do it."`, `{"type": "script", "script": "main"`}
		for _, tool := range tools {
			execs = append(execs, fmt.Sprintf(`{"type": "tool", "tool": %q`, strings.Split(tool, `"`)[3]))
		}
		exec := one(execs...)
		if strings.Contains(exec, "agent") {
			if calls := pick("deck.records_list", "deck.*", "other.thing"); r.IntN(2) == 0 {
				exec += `, "calls": ` + list(calls)
			}
			if r.IntN(3) == 0 {
				exec += `, "folder": ` + one(`true`, `"repo"`, `"docs"`)
			}
		}
		c := fmt.Sprintf(`{"name": %q, "title": %q, "description": "Does it.", "class": %q, "executor": %s}`, name, name,
			one("internal", "reversible", "external"), exec+"}")
		if req := pick("no_cloud", "images"); len(req) > 0 {
			c = strings.Replace(c, `"executor"`, `"requires": `+list(req)+`, "executor"`, 1)
		}
		if callers := pick("owner", "agent", "master"); len(callers) > 0 {
			c = strings.Replace(c, `"executor"`, `"callers": `+list(callers)+`, "executor"`, 1)
		}
		if r.IntN(3) == 0 {
			c = strings.Replace(c, `"executor"`, `"provider": `+fmt.Sprintf("%q", one("claude-code", "codex"))+`, "executor"`, 1)
		}
		cmds = append(cmds, c)
	}
	servers := map[string]string{}
	for _, name := range pick("svc", "open") {
		s := fmt.Sprintf(`{"command": %q`, one("/bin/cat", "/bin/echo"))
		if r.IntN(3) == 0 {
			s += `, "env": {"KEY": "$secret.` + one("k1", "k2") + `"}`
		}
		if f := pick("a", "b"); len(f) > 0 {
			s += `, "toolFilter": ` + list(f)
		}
		if r.IntN(2) == 0 {
			s += fmt.Sprintf(`, "classes": {"a": %q}`, one("internal", "external"))
		}
		servers[name] = s + "}"
	}
	var reads []string
	for _, project := range pick("other", "*") {
		rd := fmt.Sprintf(`{"project": %q`, project)
		if project != "*" {
			if cs := pick("items", "notes"); len(cs) > 0 {
				rd += `, "collections": ` + list(cs)
			}
			if ds := pick("a.md", "b.md"); len(ds) > 0 {
				rd += `, "documents": ` + list(ds)
			}
		}
		models, runs := r.IntN(2) == 0, r.IntN(2) == 0
		if project == "*" && !models && !runs {
			models = true
		}
		if models {
			rd += `, "models": true`
		}
		if runs {
			rd += `, "runs": true`
		}
		reads = append(reads, rd+"}")
	}
	var parts []string
	parts = append(parts, fmt.Sprintf(`"version": %d`, manifest.FormatVersion), `"scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}]`)
	if len(cmds) > 0 {
		parts = append(parts, `"commands": [`+strings.Join(cmds, ", ")+`]`)
	}
	if len(tools) > 0 || len(servers) > 0 {
		var ts []string
		if len(tools) > 0 {
			ts = append(ts, `"http": [`+strings.Join(tools, ", ")+`]`)
		}
		if len(servers) > 0 {
			var ss []string
			for _, n := range []string{"svc", "open"} {
				if s, ok := servers[n]; ok {
					ss = append(ss, fmt.Sprintf("%q: %s", n, s))
				}
			}
			ts = append(ts, `"mcp": {"servers": {`+strings.Join(ss, ", ")+`}}`)
		}
		parts = append(parts, `"tools": {`+strings.Join(ts, ", ")+`}`)
	}
	if ext := pick(names...); len(ext) > 0 {
		parts = append(parts, `"allowed_external": `+list(ext))
	}
	read, act := pick("a.example", "*", "10.0.0.1"), pick("b.example", "*")
	if len(read)+len(act) > 0 {
		parts = append(parts, fmt.Sprintf(`"domains": {"read": %s, "act": %s}`, list(read), list(act)))
	}
	if ms := pick("openrouter", "@image", "groq/fast"); len(ms) > 0 {
		parts = append(parts, `"models": `+list(ms))
	}
	if len(reads) > 0 {
		parts = append(parts, `"reads": [`+strings.Join(reads, ", ")+`]`)
	}
	if ws := pick("notes.md", "data/"); len(ws) > 0 {
		parts = append(parts, `"writes": `+list(ws))
	}
	if r.IntN(2) == 0 {
		parts = append(parts, `"no_network": true`)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// rightAtom is one right a line of what widens names, without what it had before.
func rightAtom(w string) string {
	if i := strings.Index(w, " instead of "); i >= 0 {
		w = w[:i]
	}
	if name, rest, ok := strings.Cut(w, ": new without "); ok {
		return name + ": no longer requires " + rest
	}
	if name, rest, ok := strings.Cut(w, ": new with class "); ok {
		return name + ": class " + rest
	}
	return w
}

// heldBack reports whether what widens beyond what is in force, held, keeps right w out: the line itself, or the
// read of its whole project or the whole server it is part of.
func heldBack(held []string, w string) bool {
	w = rightAtom(w)
	if slices.Contains(held, w) {
		return true
	}
	if rest, ok := strings.CutPrefix(w, "reads collection "); ok {
		return slices.Contains(held, "reads project "+rest[:strings.LastIndex(rest, ".")])
	}
	if rest, ok := strings.CutPrefix(w, "reads document "); ok {
		return slices.Contains(held, "reads project "+rest[:strings.Index(rest, "/")])
	}
	for _, prefix := range []string{"MCP server ", "MCP tool "} {
		if rest, ok := strings.CutPrefix(w, prefix); ok {
			server := rest[:strings.IndexAny(rest, ".:")]
			return slices.Contains(held, "MCP server "+server+": new or changed")
		}
	}
	return false
}

func rightAtoms(ws []string) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, rightAtom(w))
	}
	return out
}

// asksEachTime reports whether right w the grant lacks asks the owner on every call as it is in force in project:
// a command on another executor or a tool that widens is external, a secret is only of such tools or granted servers.
func asksEachTime(e *harness.Env, project string, g, next *manifest.Manifest, w string) bool {
	external := func(name string) bool {
		var c harness.CommandView
		code, raw := e.Call("deck.command_describe", map[string]any{"command": project + "." + name})
		return code == 200 && json.Unmarshal(raw, &c) == nil && c.Class == registry.External
	}
	if secret, ok := strings.CutPrefix(w, "secret: "); ok {
		for _, tool := range next.Tools.HTTP {
			if strings.Contains(fmt.Sprint(tool.Headers, string(tool.Auth)), "$secret."+secret) && !external(tool.Name) {
				return false
			}
		}
		uses := func(m *manifest.Manifest) bool {
			return slices.ContainsFunc(slices.Collect(maps.Values(m.Tools.MCP.Servers)), func(s manifest.MCPServer) bool {
				return strings.Contains(fmt.Sprint(s.Env, s.Args, s.URL, s.Command), "$secret."+secret)
			})
		}
		return !uses(next) || uses(g)
	}
	if name, ok := strings.CutPrefix(w, "command "); ok && strings.Contains(w, ": runs ") {
		return external(name[:strings.Index(name, ":")])
	}
	if name, ok := strings.CutPrefix(w, "tool "); ok && strings.Contains(w, " runs without asking") {
		return external(name[:strings.Index(name, " ")])
	}
	return false
}

// TestWhatIsInForceWidensNothingTheOwnerDidNotGrant: for many pairs of a granted manifest and a later one the owner
// never granted, what deck runs widens nothing beyond the grant but what asks the owner on every call.
func TestWhatIsInForceWidensNothingTheOwnerDidNotGrant(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("other", harness.Manifest(`{"collections": [
    {"name": "items", "description": "An item.", "key": "name", "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}},
    {"name": "notes", "description": "A note.", "key": "name", "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`))
	r := rand.New(rand.NewPCG(1, 2))
	read := 0
	for i := range harness.RightsPairs {
		granted, next := rightsManifest(r), rightsManifest(r)
		g, err := manifest.Parse([]byte(granted))
		if err != nil {
			t.Fatalf("a generated manifest does not read: %v\n%s", err, granted)
		}
		n, err := manifest.Parse([]byte(next))
		if err != nil {
			t.Fatalf("a generated manifest does not read: %v\n%s", err, next)
		}
		name := fmt.Sprintf("p%d", i)
		e.Project(name, granted)
		var beyond, inForce struct {
			Refused []string
			Widens  []string
		}
		e.Must("deck.manifest_check", map[string]any{"project": name, "manifest": next}, &beyond)
		before := e.ManifestRevision(name)
		// A revision of a deck before that the owner never granted.
		e.PlantRevision(name, "manifest.json", next)
		if e.ManifestRevision(name) == before {
			continue
		}
		read++
		e.Must("deck.manifest_check", map[string]any{"project": name}, &inForce)
		if len(beyond.Widens) == 0 && len(beyond.Refused) > 0 {
			t.Fatalf("pair %d: the check reads no rights of the revision: %v\n%s", i, beyond.Refused, next)
		}
		held := rightAtoms(inForce.Widens)
		for _, w := range beyond.Widens {
			if heldBack(held, w) || asksEachTime(e, name, g, n, w) {
				continue
			}
			t.Errorf("pair %d: what is in force widens %q\ngranted: %s\nnext: %s", i, w, granted, next)
		}
	}
	if read < harness.RightsPairs*4/5 {
		t.Fatalf("too few pairs reached a revision beyond the grant: %d", read)
	}
}

// studioModule has one agent command that edits the code of the module.
var studioModule = harness.Manifest(`{"writes": ["code/"],
  "commands": [
    {"name": "build", "title": "Build", "description": "Edits the code.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Edit the code."}}]}`)

// TestAnAgentWritesTheDocumentsItsManifestNames: an agent saves a document writes names and changes it without its
// kind, is refused another with what to add, knows both from its prompt, and a wider writes waits for the owner.
func TestAnAgentWritesTheDocumentsItsManifestNames(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	outs := map[string]string{}
	var prompt string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		save := func(key string, args map[string]any) {
			out, bad := c.Tool(ctx, "document_save", args)
			if bad {
				out = "refused: " + out
			}
			mu.Lock()
			outs[key] = out
			mu.Unlock()
		}
		mu.Lock()
		prompt = c.Req.Prompt
		mu.Unlock()
		save("new", map[string]any{"name": "code/scene.jsx", "kind": "doc", "text": "export default 1"})
		save("again", map[string]any{"name": "code/scene.jsx", "old": []string{"1"}, "new": []string{"2"}})
		save("other", map[string]any{"name": "notes.md", "kind": "doc", "text": "hi"})
		save("unknown", map[string]any{"name": "code/none.jsx", "text": "x"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Project("studio", studioModule)
	e.WaitState(e.Start("studio", "build", nil), "done")
	mu.Lock()
	defer mu.Unlock()
	if strings.HasPrefix(outs["new"], "refused") || strings.HasPrefix(outs["again"], "refused") {
		t.Fatalf("the agent writes a document under writes and changes it without its kind: %v", outs)
	}
	if !strings.Contains(outs["other"], "refused") || !strings.Contains(outs["other"], "writes in its manifest") {
		t.Fatalf("another document is refused with what to add: %s", outs["other"])
	}
	if !strings.Contains(outs["unknown"], "give its kind to create it") {
		t.Fatalf("a new document without a kind is refused: %s", outs["unknown"])
	}
	if !strings.Contains(prompt, "these documents of the project: code/") || !strings.Contains(prompt, "do not start the work") {
		t.Fatalf("the agent knows what it may write before it works: %s", prompt)
	}
	if !strings.Contains(prompt, "do not call deck's HTTP API") {
		t.Fatalf("the agent is told to reach deck's data through its tools: %s", prompt)
	}
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "studio", "name": "code/scene.jsx"}, &doc)
	if doc.Text != "export default 2" {
		t.Fatalf("the change is saved: %q", doc.Text)
	}
	var feed []struct{ Text string }
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if !slices.ContainsFunc(feed, func(f struct{ Text string }) bool { return strings.Contains(f.Text, "changed document code/scene.jsx") }) {
		t.Fatalf("the owner sees the change with an undo: %+v", feed)
	}

	_, tok := e.Token("terminal", "deck.*")
	var w struct {
		Waiting int64
		Widens  []string
	}
	wider := strings.Replace(studioModule, `"writes": ["code/"]`, `"writes": ["code/", "personas/style.md"]`, 1)
	code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "studio", "manifest": wider})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"writes document personas/style.md"}) {
		t.Fatalf("a wider writes waits for the owner's button: %d %s", code, out)
	}
	narrower := strings.Replace(studioModule, `"writes": ["code/"]`, `"writes": ["code/scene.jsx"]`, 1)
	if code, out := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "studio", "manifest": narrower}); code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("a narrower writes needs no button: %d %s", code, out)
	}
}

// TestAnAgentDeletesTheRecordsOfItsOwnProject: an agent of a module deletes a record of its project, which the owner
// may undo, and not a record of another project.
func TestAnAgentDeletesTheRecordsOfItsOwnProject(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	outs := map[string]string{}
	var own, other int64
	e := harness.ClipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "tally" {
			for key, id := range map[string]int64{"own": own, "other": other} {
				out, bad := c.Tool(ctx, "record_delete", map[string]any{"record": id})
				if bad {
					out = "refused: " + out
				}
				mu.Lock()
				outs[key] = out
				mu.Unlock()
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.Project("reels", harness.ClipsModule)
	own = e.Clip("a")
	e.Must("deck.records_upsert", map[string]any{"project": "reels", "collection": "clips", "records": harness.Jsonl(map[string]any{"slug": "b"})}, nil)
	var reels []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "reels", "collection": "clips"}, &reels)
	other = reels[0].ID
	e.WaitState(e.Start("clips", "tally", nil), "done")
	mu.Lock()
	defer mu.Unlock()
	if strings.HasPrefix(outs["own"], "refused") {
		t.Fatalf("an agent deletes a record of its own project: %s", outs["own"])
	}
	if !strings.HasPrefix(outs["other"], "refused") {
		t.Fatalf("an agent does not delete a record of another project: %s", outs["other"])
	}
	var feed []struct {
		Text    string
		Actions []map[string]any
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) == 0 || !strings.Contains(feed[0].Text, "deleted") {
		t.Fatalf("the owner sees the deletion with an undo: %+v", feed)
	}
}

var probeModule = harness.Manifest(`{"commands": [{"name": "probe", "title": "Probe", "description": "Reads settings.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "probe"}}],
  "scripts": [{"name": "probe", "file": "scripts/probe.py", "runtime": "python"}]}`)

// probeScript calls a command of the owner side and fails on the bare status, as a script that reads no body does.
const probeScript = `import json, os, urllib.request
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/deck.settings_list", data=b"{}",
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
urllib.request.urlopen(req, timeout=30)
print("{}")
`

// TestAScriptFailedByARefusalSaysWhatDeckRefused: a script that dies on a bare 403 fails with what deck refused and
// who may call it, as a failure of rights.
func TestAScriptFailedByARefusalSaysWhatDeckRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("probe", probeModule)
	e.SaveScript("probe", "scripts/probe.py", probeScript)
	id := e.Start("probe", "probe", nil)
	e.WaitState(id, "done", "error")
	f := e.Fault(id)
	if f.ErrorType != "rights.denied" || !strings.Contains(f.Error, "deck refused its calls: deck.settings_list") ||
		!strings.Contains(f.Error, "cannot be called by agent (script); only owner, master may call it") {
		t.Fatalf("the failure names the refusal: %+v", f)
	}
}

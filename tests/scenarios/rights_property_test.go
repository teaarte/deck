package scenarios

import (
	"context"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
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
	parts = append(parts, `"version": 13`, `"scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}]`)
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

// asksEachTime reports whether widening w of what is in force asks the owner on every call: a command on another
// executor with class external, a secret only tools that ask or granted servers use.
func asksEachTime(g, in *manifest.Manifest, w string) bool {
	if secret, ok := strings.CutPrefix(w, "secret: "); ok {
		for _, tool := range in.Tools.HTTP {
			asks := tool.Class == registry.External && !slices.Contains(in.AllowedExternal, tool.Name)
			if !asks && strings.Contains(fmt.Sprint(tool.Headers, string(tool.Auth)), "$secret."+secret) {
				return false
			}
		}
		uses := func(m *manifest.Manifest) bool {
			return slices.ContainsFunc(slices.Collect(maps.Values(m.Tools.MCP.Servers)), func(s manifest.MCPServer) bool {
				return strings.Contains(fmt.Sprint(s.Env, s.Args, s.URL, s.Command), "$secret."+secret)
			})
		}
		return !uses(in) || uses(g)
	}
	if name, ok := strings.CutPrefix(w, "command "); ok && strings.Contains(w, ": runs ") {
		c := in.Command(name[:strings.Index(name, ":")])
		return c != nil && c.Class == registry.External && !slices.Contains(in.AllowedExternal, c.Name)
	}
	return false
}

// TestWhatIsInForceWidensNothingTheOwnerDidNotGrant: for many pairs of a granted manifest and a later one the owner
// never granted, what deck runs widens nothing beyond the grant but what asks the owner on every call.
func TestWhatIsInForceWidensNothingTheOwnerDidNotGrant(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	ctx := context.Background()
	e.Project("other", `{"version": 13, "collections": [
    {"name": "items", "description": "An item.", "key": "name", "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}},
    {"name": "notes", "description": "A note.", "key": "name", "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`)
	r := rand.New(rand.NewPCG(1, 2))
	read := 0
	for i := range harness.RightsPairs {
		granted, next := rightsManifest(r), rightsManifest(r)
		if _, err := manifest.Parse([]byte(granted)); err != nil {
			t.Fatalf("a generated manifest does not read: %v\n%s", err, granted)
		}
		if _, err := manifest.Parse([]byte(next)); err != nil {
			t.Fatalf("a generated manifest does not read: %v\n%s", err, next)
		}
		name := fmt.Sprintf("p%d", i)
		e.Project(name, granted)
		p, err := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		err = e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, _, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}, docs.KindManifest, next, "test", "a revision never granted")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		g, err := docs.ManifestAt(ctx, e.App.Store.Q, p.ID, p.GrantedRevision.Int64)
		if err != nil {
			t.Fatal(err)
		}
		in, err := docs.For(ctx, e.App.Store.Q, registry.Actor{}, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if in.Revision == g.Revision {
			continue
		}
		read++
		for _, w := range manifest.Widens(g, in) {
			if asksEachTime(g, in, w) {
				continue
			}
			t.Errorf("pair %d: what is in force widens %q\ngranted: %s\nnext: %s", i, w, granted, next)
		}
	}
	if read < harness.RightsPairs*4/5 {
		t.Fatalf("too few pairs reached a revision beyond the grant: %d", read)
	}
}

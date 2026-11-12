package store

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"deck2/schemas"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pressly/goose/v3"
)

// Rights17 is the mark of a workspace whose modules format 17 moved to deck's catalog of rights: what it granted
// always, whose data may go to the network and whose agents no longer reach it, the text of a note to the owner.
const Rights17 = "rights:format17"

// format17 raises the stored manifests and those of open asks to format 17, the rights of deck's catalog declared: what
// worked is granted always, but the network in the shells of agents on a subscription login, which no turn has now.
var format17 = goose.NewGoMigration(82, &goose.GoFunc{RunTx: toFormat17}, nil)

// grant17 is an answer of always format 17 carries over from what the owner had granted.
type grant17 struct {
	right, who, subject, print string
}

// decl17 is a right a manifest of format 16 makes its module declare; works says it ran without the owner then.
type decl17 struct {
	right, who, subject, why, print string
	works                           bool
}

// cliProviders are the providers of a command that run, or may run, on a subscription login.
var cliProviders = []string{"", "claude-code", "codex", "free"}

// agentHosts are what the shell of an agent with any domain reached in format 16 beside them.
var agentHosts = []string{"pypi.org", "files.pythonhosted.org", "registry.npmjs.org", "proxy.golang.org", "sum.golang.org",
	"github.com", "codeload.github.com", "objects.githubusercontent.com", "huggingface.co", "hf.co"}

type format16Manifest struct {
	Version         json.Number `json:"version"`
	AllowedExternal []string    `json:"allowed_external"`
	Domains         struct {
		Read []string `json:"read"`
		Act  []string `json:"act"`
	} `json:"domains"`
	Reads []struct {
		Project     string   `json:"project"`
		Collections []string `json:"collections"`
		Documents   []string `json:"documents"`
		Models      bool     `json:"models"`
		Runs        bool     `json:"runs"`
	} `json:"reads"`
	Models   []string `json:"models"`
	GPU      bool     `json:"gpu"`
	Commands []struct {
		Name     string `json:"name"`
		Class    string `json:"class"`
		Provider string `json:"provider"`
		Executor struct {
			Type   string `json:"type"`
			Tool   string `json:"tool"`
			Model  string `json:"model"`
			Kind   string `json:"kind"`
			Folder any    `json:"folder"`
		} `json:"executor"`
	} `json:"commands"`
	Tools struct {
		HTTP []map[string]json.RawMessage `json:"http"`
		MCP  struct {
			Servers map[string]struct {
				Classes map[string]string `json:"classes"`
			} `json:"servers"`
		} `json:"mcp"`
	} `json:"tools"`
}

// mild says a class of format 16 ran without asking.
func mild(class string) bool { return class == "internal" || class == "reversible" }

// rights16 are the rights a manifest of format 16 declares in format 17 and whether each worked without the owner;
// approved are the prints of MCP tools the owner allowed, by server.tool. ok is false for a text of another format.
func rights16(text string, approved map[string]string) (out []decl17, ok bool) {
	var m format16Manifest
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	if d.Decode(&m) != nil || m.Version.String() != "16" {
		return nil, false
	}
	add := func(right, who, subject, why, print string, works bool) {
		for i, o := range out {
			if o.right == right && o.who == who && o.subject == subject {
				out[i].works = o.works || works
				return
			}
		}
		out = append(out, decl17{right, who, subject, why, print, works})
	}
	listed := func(name string) bool { return slices.Contains(m.AllowedExternal, name) }
	for _, h := range append(slices.Clone(m.Domains.Read), m.Domains.Act...) {
		add("network", "programs", strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "*."), "its domains in format 16", "", true)
	}
	logins, agents := []string{}, false
	for _, c := range m.Commands {
		if c.Executor.Type != "agent" {
			continue
		}
		agents = true
		base, _, _ := strings.Cut(c.Provider, "@")
		switch {
		case base == "claude-code" || base == "codex":
			logins = append(logins, base)
		case slices.Contains(cliProviders, base):
			logins = append(logins, "claude-code", "codex")
		}
	}
	slices.Sort(logins)
	for _, l := range slices.Compact(logins) {
		add("login", "", l, "its agents ran on this login in format 16", "", true)
	}
	if agents && len(logins) == 0 && len(m.Domains.Read) > 0 {
		for _, h := range append(slices.Clone(m.Domains.Read), agentHosts...) {
			add("network", "agent", strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "*."), "the shells of its agents reached it in format 16", "", true)
		}
	}
	for _, r := range m.Reads {
		why := "its reads in format 16"
		if r.Models {
			add("data", "", r.Project+":models", why, "", true)
		}
		if r.Runs {
			add("data", "", r.Project+":runs", why, "", true)
		}
		if r.Project == "*" || (r.Models || r.Runs) && len(r.Collections)+len(r.Documents) == 0 {
			continue
		}
		for _, c := range r.Collections {
			add("data", "", r.Project+"."+c, why, "", true)
		}
		for _, doc := range r.Documents {
			add("data", "", r.Project+"/"+doc, why, "", true)
		}
		if len(r.Collections)+len(r.Documents) == 0 {
			add("data", "", r.Project, why, "", true)
		}
	}
	if m.GPU {
		add("files", "", "gpu", "it used the graphics card in format 16", "", true)
	}
	uses := slices.Clone(m.Models)
	for _, c := range m.Commands {
		switch c.Executor.Type {
		case "model":
			uses = append(uses, cmp.Or(c.Executor.Model, "@"+c.Executor.Kind))
		case "agent":
			switch f := c.Executor.Folder.(type) {
			case string:
				add("files", "", f, "its agents worked in this folder in format 16", "", true)
			case bool:
				if f {
					add("files", "", "*", "its agents worked in its folder in format 16", "", true)
				}
			}
		}
	}
	for _, u := range uses {
		add("money", "", u, "it called models through this in format 16", "", true)
	}
	for _, c := range m.Commands {
		if c.Class == "external" || c.Class == "reversible" {
			add("send", "", c.Name, "it was "+c.Class+" in format 16", c.Executor.Type+"/"+c.Executor.Tool, c.Class == "reversible" || listed(c.Name))
		}
	}
	for _, t := range m.Tools.HTTP {
		var name, method, class string
		_ = json.Unmarshal(t["name"], &name)
		_ = json.Unmarshal(t["method"], &method)
		_ = json.Unmarshal(t["class"], &class)
		works := mild(class) || listed(name)
		right, subject, print := tool16(t, name, method)
		add(right, cmpWho(right), subject, "its tool "+name+" in format 16", print, works)
	}
	for server, s := range m.Tools.MCP.Servers {
		for tool, class := range s.Classes {
			if class == "external" || class == "reversible" {
				name := server + "." + tool
				add("send", "", name, "its MCP tool in format 16", approved[name], approved[name] != "" && (class == "reversible" || listed(name)))
			}
		}
	}
	for _, n := range m.AllowedExternal {
		if strings.HasPrefix(n, deck) {
			add("send", "", n, "it was allowed external in format 16", "", true)
		}
	}
	for _, h := range m.Domains.Act {
		add("send", "", strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "*."), "the browser acted on this site in format 16", "", true)
	}
	return out, true
}

func cmpWho(right string) string {
	if right == "network" {
		return "programs"
	}
	return ""
}

// tool16 is the right an HTTP tool of format 16 needs in format 17, its subject and print: network on its host for GET
// or HEAD without a secret, data on the tool with one, send for another method.
func tool16(t map[string]json.RawMessage, name, method string) (right, subject, print string) {
	ref := []byte(schemas.SecretPrefix)
	keyed := bytes.Contains(t["url"], ref) || bytes.Contains(t["headers"], ref) || bytes.Contains(t["body"], ref) || len(t["auth"]) > 0
	call := map[string]any{}
	for _, k := range []string{"method", "url", "hosts", "headers", "body", "auth"} {
		call[k] = t[k]
		if t[k] == nil {
			call[k] = nil
		}
	}
	b, _ := json.Marshal(call)
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if d.Decode(&v) == nil {
		b, _ = json.Marshal(v)
	}
	if method != "GET" && method != "HEAD" {
		return "send", name, string(b)
	}
	if keyed {
		return "data", name, string(b)
	}
	var u string
	_ = json.Unmarshal(t["url"], &u)
	host := u
	if _, rest, ok := strings.Cut(u, "://"); ok {
		host = rest
	}
	host, _, _ = strings.Cut(host, "/")
	host, _, _ = strings.Cut(host, "?")
	if h, _, ok := strings.Cut(host, "@"); ok {
		host = h
	}
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	return "network", strings.ToLower(host), ""
}

// manifest17 is a manifest of format 16 in format 17: its rights declared from the fields that go, without them; any
// other text as it is.
func manifest17(text string, approved map[string]string) string {
	decls, ok := rights16(text, approved)
	top, mok := members([]byte(text))
	if !ok || !mok {
		return text
	}
	var rights []json.RawMessage
	for _, d := range groupRights(decls) {
		rights = append(rights, d)
	}
	var out []member
	placed := false
	for _, m := range top {
		switch m.key {
		case "version":
			m.val = json.RawMessage("17")
		case "allowed_external", "domains", "reads", "models", "gpu":
			if !placed && len(rights) > 0 {
				out = append(out, member{"rights", array(rights)})
			}
			placed = true
			continue
		case "commands":
			m.val = without(m.val, "class")
		case "tools":
			m.val = tools17(m.val)
		}
		out = append(out, m)
	}
	if !placed && len(rights) > 0 {
		out = append(out, member{"rights", array(rights)})
	}
	var buf bytes.Buffer
	if json.Indent(&buf, object(out), "", "  ") != nil {
		return text
	}
	return buf.String()
}

// groupRights are declarations as format 17 writes them: one entry for a right, whom and why, its subjects in order.
func groupRights(ds []decl17) []json.RawMessage {
	type key struct{ right, who, why string }
	var order []key
	what := map[key][]string{}
	for _, d := range ds {
		if d.subject == "" {
			continue
		}
		k := key{d.right, d.who, d.why}
		if _, ok := what[k]; !ok {
			order = append(order, k)
		}
		what[k] = append(what[k], d.subject)
	}
	var out []json.RawMessage
	for _, k := range order {
		ms := []member{{"right", quote(k.right)}, {"what", mustJSON(what[k])}, {"why", quote(k.why)}}
		if k.who == "agent" {
			ms = append(ms, member{"who", quote("agent")})
		}
		out = append(out, object(ms))
	}
	return out
}

func quote(s string) json.RawMessage { return mustJSON(s) }

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// without is a list of objects without the member key in each.
func without(list json.RawMessage, key string) json.RawMessage {
	var items []json.RawMessage
	if json.Unmarshal(list, &items) != nil {
		return list
	}
	for i, it := range items {
		if ms, ok := members(it); ok {
			items[i] = object(slices.DeleteFunc(ms, func(m member) bool { return m.key == key }))
		}
	}
	return array(items)
}

// tools17 are the tools of format 16 without classes: an MCP tool of class internal reads, network in format 17.
func tools17(raw json.RawMessage) json.RawMessage {
	ms, ok := members(raw)
	if !ok {
		return raw
	}
	for i, m := range ms {
		switch m.key {
		case "http":
			ms[i].val = without(m.val, "class")
		case "mcp":
			mcp, ok := members(m.val)
			if !ok {
				continue
			}
			for j, sm := range mcp {
				if sm.key != "servers" {
					continue
				}
				servers, ok := members(sm.val)
				if !ok {
					continue
				}
				for k, s := range servers {
					fields, ok := members(s.val)
					if !ok {
						continue
					}
					for l, f := range fields {
						if f.key != "classes" {
							continue
						}
						var classes map[string]string
						_ = json.Unmarshal(f.val, &classes)
						right := map[string]string{}
						for tool, c := range classes {
							if c == "internal" {
								right[tool] = "network"
							}
						}
						if len(right) == 0 {
							fields = slices.Delete(fields, l, l+1)
						} else {
							fields[l] = member{"right", mustJSON(right)}
						}
						break
					}
					servers[k].val = object(fields)
				}
				mcp[j].val = object(servers)
			}
			ms[i].val = object(mcp)
		}
	}
	return object(ms)
}

func toFormat17(ctx context.Context, tx *sql.Tx) error {
	approved := map[int64]map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT project_id, server || '.' || name, approved FROM mcp_tool WHERE approved = fingerprint`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var project int64
		var name, print string
		if err := rows.Scan(&project, &name, &print); err != nil {
			_ = rows.Close()
			return err
		}
		if approved[project] == nil {
			approved[project] = map[string]string{}
		}
		approved[project][name] = print
	}
	if err := rows.Close(); err != nil {
		return err
	}
	type project struct {
		id, workspace     int64
		name              string
		builtin           bool
		current, granted  string
		hasGrant, enabled bool
	}
	var ps []project
	rows, err = tx.QueryContext(ctx, `SELECT p.id, p.workspace_id, p.name, p.builtin = 1, p.status = 'enabled',
		COALESCE((SELECT r.text FROM document_revision r WHERE r.document_id = d.id AND r.n = d.current), ''),
		COALESCE((SELECT r.text FROM document_revision r WHERE r.document_id = d.id AND r.n = p.granted_revision), ''),
		p.granted_revision IS NOT NULL
		FROM project p JOIN "document" d ON d.project_id = p.id AND d.kind = 'manifest' AND d.deleted_at IS NULL ORDER BY p.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p project
		if err := rows.Scan(&p.id, &p.workspace, &p.name, &p.builtin, &p.enabled, &p.current, &p.granted, &p.hasGrant); err != nil {
			_ = rows.Close()
			return err
		}
		ps = append(ps, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	notes := map[int64][]string{}
	for _, p := range ps {
		if p.builtin {
			continue
		}
		now, ok := rights16(p.current, approved[p.id])
		if !ok {
			continue
		}
		var given []decl17
		if p.hasGrant {
			given, _ = rights16(p.granted, approved[p.id])
		}
		var always, agentNet []string
		for _, d := range now {
			if !d.works || !slices.ContainsFunc(given, func(g decl17) bool {
				return g.works && g.right == d.right && g.who == d.who && g.subject == d.subject
			}) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO right_grant (project_id, name, who, subject, answer, print, created_at)
				VALUES (?, ?, ?, ?, 'always', ?, ?) ON CONFLICT DO NOTHING`, p.id, d.right, d.who, d.subject, d.print, migratedAt); err != nil {
				return err
			}
			always = append(always, kindOf(d)+" "+d.subject)
			if d.who == "agent" {
				agentNet = append(agentNet, d.subject)
			}
		}
		var lines []string
		if len(always) > 0 {
			lines = append(lines, fmt.Sprintf("%s keeps always what you had granted: %s.", p.name, grouped(always)))
		}
		if len(agentNet) > 0 {
			lines = append(lines, fmt.Sprintf("The agents of %s reach %s on their own, so its data and folders may go there.", p.name, strings.Join(agentNet, ", ")))
		}
		if lost := lostNetwork(p.current); len(lost) > 0 {
			lines = append(lines, fmt.Sprintf("The agents of %s run on a subscription login and no longer reach the network from their shells (%s): its scripts and deck's commands do.", p.name, strings.Join(lost, ", ")))
		}
		notes[p.workspace] = append(notes[p.workspace], lines...)
	}
	for ws, lines := range notes {
		if len(lines) == 0 {
			continue
		}
		text := "Modules now declare the rights of deck's catalog, and your answers live apart from their manifests: what you had granted is kept as always, the rest asks you when first used.\n- " +
			strings.Join(lines, "\n- ")
		if _, err := tx.ExecContext(ctx, `INSERT INTO mark (workspace_id, name, value) VALUES (?, ?, ?)
			ON CONFLICT (workspace_id, name) DO UPDATE SET value = excluded.value`, ws, Rights17, text); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mark WHERE name LIKE ?`, Widened+"%"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ask SET state = 'expired' WHERE kind = 'grant' AND state = 'open'`); err != nil {
		return err
	}
	byDoc := map[int64]int64{}
	rows, err = tx.QueryContext(ctx, `SELECT r.id, d.project_id FROM document_revision r JOIN "document" d ON d.id = r.document_id
		WHERE d.kind = 'manifest' AND d.project_id IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			_ = rows.Close()
			return err
		}
		byDoc[id] = p
	}
	if err := rows.Close(); err != nil {
		return err
	}
	err = renameColumn(ctx, tx, "document_revision", "text", func(id int64, v string) string {
		p, ok := byDoc[id]
		if !ok {
			return v
		}
		return manifest17(v, approved[p])
	})
	if err != nil {
		return err
	}
	return renameColumn(ctx, tx, "ask", "payload", func(_ int64, v string) string {
		var p struct {
			Call struct {
				Input struct {
					Manifest string `json:"manifest"`
				} `json:"input"`
			} `json:"call"`
		}
		if json.Unmarshal([]byte(v), &p) != nil || p.Call.Input.Manifest == "" {
			return v
		}
		raised := manifest17(p.Call.Input.Manifest, nil)
		if raised == p.Call.Input.Manifest {
			return v
		}
		was, _ := json.Marshal(p.Call.Input.Manifest)
		now, _ := json.Marshal(raised)
		return strings.Replace(v, string(was), string(now), 1)
	})
}

// migratedAt is when format 17 granted what it carried over, as no clock is read in a migration.
const migratedAt = "2026-11-12T00:00:00.000Z"

// lostNetwork are the domains the shells of the agents of a module on a subscription login reached in format 16 and
// no longer do.
func lostNetwork(text string) []string {
	ds, ok := rights16(text, nil)
	if !ok || !slices.ContainsFunc(ds, func(d decl17) bool { return d.right == "login" }) {
		return nil
	}
	var m format16Manifest
	if json.Unmarshal([]byte(text), &m) != nil || len(m.Domains.Read) == 0 {
		return nil
	}
	return m.Domains.Read
}

// UpgradeManifest raises a manifest of an older format a module brings from outside, an import or a starter, to the
// current one by the steps the stored ones took; any other text as it is. It grants nothing.
func UpgradeManifest(text string) string {
	text = manifest15(format14(rename(text, true)))
	return manifest17(manifest16(text, nil), nil)
}

// kindOf is a right as the note of format 17 names it: the network of agents apart from that of programs.
func kindOf(d decl17) string {
	if d.who == "agent" {
		return "network of its agents"
	}
	return d.right
}

// grouped are "right subject" items as one list a right: "network a, b; login c".
func grouped(items []string) string {
	var order []string
	subjects := map[string][]string{}
	for _, it := range items {
		i := strings.LastIndex(it, " ")
		k, s := it[:i], it[i+1:]
		if _, ok := subjects[k]; !ok {
			order = append(order, k)
		}
		subjects[k] = append(subjects[k], s)
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, k+" "+strings.Join(subjects[k], ", "))
	}
	return strings.Join(parts, "; ")
}

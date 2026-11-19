package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/tools"
)

const maxProblems = 20

// unassigned are the warnings of the model commands of a manifest that no model of the settings takes.
func (s *Service) unassigned(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	var out []string
	for name, text := range s.Unassigned(ctx, p.WorkspaceID, p.ID, m) {
		out = append(out, "command "+name+": "+text)
	}
	slices.Sort(out)
	return out
}

// untold are the warnings of a module's commands that do not say what they changed or call one whose answer no schema
// tells, and of its scripts that keep a model in deck or are not Python; the built-in project tells the owner itself.
func (s *Service) untold(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	if p.Builtin == 1 {
		return nil
	}
	return slices.Concat(m.Unreported(), m.Unshaped(p.Name), s.heavy(ctx, p, m))
}

// heavy are the warnings of the scripts of a module that keep a model in deck or are not Python, read with their files.
func (s *Service) heavy(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	return m.Heavy(func(file string) string {
		if p.ID == 0 {
			return ""
		}
		text, _ := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindScript, file)
		return text
	})
}

// unpaged are the blocks without a limit on a collection that holds more records than a page: each shows a page and
// more by a button.
func (s *Service) unpaged(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	counts, err := s.St.Q.CountRecords(ctx, p.ID)
	if err != nil {
		return nil
	}
	var out []string
	blocks := m.Unpaged()
	for _, c := range counts {
		for _, b := range blocks[c.Collection] {
			if c.N > manifest.Page {
				out = append(out, fmt.Sprintf("%s has no limit and %s holds %d records: it shows %d at a time; give its source a limit", b, c.Collection, c.N, manifest.Page))
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkToolFit checks tool commands whose tool is a builtin or an MCP tool deck has listed: the command
// input must go to the tool as it is. An MCP tool deck has not seen yet is checked when the command runs.
func (s *Service) checkToolFit(ctx context.Context, q *db.Queries, workspace, projectID int64, m *manifest.Manifest) error {
	var listed []db.McpTool
	if projectID != 0 {
		var err error
		if listed, err = q.ListMCPTools(ctx, projectID); err != nil {
			return err
		}
	}
	for _, c := range m.Commands {
		if c.Executor.Type != "tool" {
			continue
		}
		name := c.Executor.Tool
		var input json.RawMessage
		if registry.IsBuiltin(name) {
			t, err := s.Registry.Lookup(ctx, workspace, name)
			if err != nil {
				return registry.Invalid("command %q: no builtin tool %q", c.Name, name)
			}
			input = t.Input
		} else if server, tool, ok := strings.Cut(name, "."); ok {
			srv := m.Tools.MCP.Servers[server]
			i := slices.IndexFunc(listed, func(r db.McpTool) bool {
				return r.Server == server && r.Name == tool && r.Listed == 1 && r.Spec == tools.SpecID(srv)
			})
			if i < 0 {
				continue
			}
			input = json.RawMessage(listed[i].Input)
		} else {
			continue
		}
		if err := manifest.FitsTool(c, name, input); err != nil {
			return registry.Invalid("command %q: %v", c.Name, err)
		}
	}
	return nil
}

// checkRecords refuses a manifest whose collections no longer fit the stored records, or the migrated
// records of the collections in plan.
func checkRecords(ctx context.Context, q *db.Queries, p db.Project, next *manifest.Manifest, plan map[string]*migration) error {
	counts, err := q.CountRecords(ctx, p.ID)
	if err != nil {
		return err
	}
	old, err := docs.Manifest(ctx, q, p)
	if err != nil {
		slog.Warn("current manifest does not parse; checking every record", "project", p.Name, "err", err)
		old = nil
	}
	var msgs []string
	for name := range plan {
		if !slices.ContainsFunc(counts, func(c db.CountRecordsRow) bool { return c.Collection == name }) {
			counts = append(counts, db.CountRecordsRow{Collection: name})
		}
	}
	for _, cnt := range counts {
		c := next.Collection(cnt.Collection)
		if c == nil {
			msgs = append(msgs, fmt.Sprintf("collection %q is removed but has %d records; delete them first with deck.collection_delete", cnt.Collection, cnt.N))
			continue
		}
		if mg := plan[c.Name]; mg != nil {
			if mg.checked {
				continue
			}
			if msg := misfitsOf(c, migratedRows(mg), false, "the migrated records do not fit; fix the migration script"); msg != "" {
				msgs = append(msgs, msg)
			}
			continue
		}
		if old != nil && sameShape(old.Collection(cnt.Collection), c) {
			continue
		}
		recs, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: c.Name})
		if err != nil {
			return err
		}
		if msg := misfitsOf(c, storedRows(recs), true, "add a migration script to this revision"); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) > 0 {
		return registry.Invalid("%s", strings.Join(msgs, "\n"))
	}
	return nil
}

func sameShape(old, next *manifest.Collection) bool {
	if old == nil {
		return false
	}
	return old.Key == next.Key && compactEqual(old.Schema, next.Schema)
}

func compactEqual(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// row is a record to check: stored ones have a key, migrated ones get theirs from the new key expression.
type row struct {
	id   int64
	key  string
	data string
}

func storedRows(recs []db.Record) []row {
	out := make([]row, len(recs))
	for i, r := range recs {
		out[i] = row{r.ID, r.Key, r.Data}
	}
	return out
}

func migratedRows(mg *migration) []row {
	out := make([]row, len(mg.rows))
	for i, r := range mg.rows {
		out[i] = row{id: r.ID, data: string(r.Data)}
	}
	return out
}

// misfitsOf describes the rows the schema or key of a collection would break; "" when all fit. A fixed key
// must stay what it is.
func misfitsOf(c *manifest.Collection, rows []row, fixedKey bool, fix string) string {
	var problems []string
	bad := 0
	keys := map[string]string{}
	for i, r := range rows {
		name := fmt.Sprintf("record %d (key %q)", r.id, r.key)
		if !fixedKey {
			name = fmt.Sprintf("line %d (record %d)", i+1, r.id)
		}
		var why string
		data, err := manifest.DecodeObject([]byte(r.data))
		switch {
		case err != nil:
			why = "data is not a JSON object"
		case c.Validate(data) != nil:
			why = c.Validate(data).Error()
		default:
			key, err := c.KeyOf(data)
			switch {
			case err != nil:
				why = err.Error()
			case fixedKey && key != r.key:
				why = fmt.Sprintf("key would change from %q to %q", r.key, key)
			case keys[key] != "":
				why = fmt.Sprintf("key %q collides with %s", key, keys[key])
			default:
				keys[key] = name
			}
		}
		if why == "" {
			continue
		}
		bad++
		if len(problems) < maxProblems {
			problems = append(problems, fmt.Sprintf("  - %s: %s", name, why))
		}
	}
	if bad == 0 {
		return ""
	}
	return fmt.Sprintf("collection %q: %d of %d records do not fit the new schema or key; %s. First problems:\n%s",
		c.Name, bad, len(rows), fix, strings.Join(problems, "\n"))
}

// checkData verifies what a manifest declares it reads of other modules: modules there are, their collections, never
// the master's data or memory, every project only for its models and runs; a service of its own named like no module.
func checkData(ctx context.Context, q *db.Queries, at db.Project, m *manifest.Manifest) error {
	if at.Builtin == 1 {
		return nil
	}
	workspace, name := at.WorkspaceID, at.Name
	for _, subject := range m.Services() {
		tool, _, _ := strings.Cut(subject, ".")
		if tool == name {
			continue
		}
		if _, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: tool}); err == nil {
			return registry.Invalid("rights: data on %s: its tool or server is named like module %q, whose data it would read as its own; rename the tool or server", subject, tool)
		}
	}
	for _, subject := range m.Others() {
		project, rest, _ := strings.Cut(subject, ":")
		project, document, _ := strings.Cut(project, "/")
		project, collection, _ := strings.Cut(project, ".")
		switch {
		case project == "*" && rest != "":
			continue
		case project == "*":
			return registry.Invalid(`rights: data on * is every module's data, which only the master reads; name the modules, or *:models and *:runs`)
		case project == name:
			return registry.Invalid("rights: data on %s: a module reads its own data without a right", subject)
		}
		p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: project})
		if err != nil {
			return registry.Invalid("rights: data on %s: no module %q", subject, project)
		}
		if p.Builtin == 1 {
			return registry.Invalid("rights: data on %s: module %s keeps the owner's memory and topics; no module reads it", subject, p.Name)
		}
		if strings.HasPrefix(document, docs.MemoryDir) {
			return registry.Invalid("rights: data on %s: the memory of %s is for its own agents and the master only", subject, project)
		}
		if collection == "" {
			continue
		}
		other, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return registry.Invalid("rights: data on %s: module %q has an invalid manifest: %v", subject, project, err)
		}
		if other.Collection(collection) == nil {
			return registry.Invalid("rights: data on %s: module %q has no collection %q", subject, project, collection)
		}
	}
	return nil
}

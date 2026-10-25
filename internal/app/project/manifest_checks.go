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

// checkReads verifies the projects and collections a manifest reads.
func checkReads(ctx context.Context, q *db.Queries, workspace int64, name string, m *manifest.Manifest) error {
	for _, r := range m.Reads {
		onlyModels := !r.Data()
		switch {
		case r.Project == name:
			return registry.Invalid("deck: reads: a project does not need to read itself")
		case r.Project == manifest.AnyProject && !onlyModels:
			return registry.Invalid(`deck: reads: %s names every project only for which models do their work and their runs: {"project": "*", "models": true, "runs": true}`, manifest.AnyProject)
		case r.Project == manifest.AnyProject:
			continue
		}
		p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: r.Project})
		if err != nil {
			return registry.Invalid("deck: reads: no project %q", r.Project)
		}
		if p.Builtin == 1 && (!onlyModels || r.Runs) {
			return registry.Invalid("deck: reads: project %s keeps the owner's memory and topics; no project reads it", p.Name)
		}
		for _, d := range r.Documents {
			if strings.HasPrefix(d, docs.MemoryDir) {
				return registry.Invalid("deck: reads: the memory of %s is for its own agents and the master only", r.Project)
			}
		}
		other, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return registry.Invalid("deck: reads: project %q has an invalid manifest: %v", r.Project, err)
		}
		for _, c := range r.Collections {
			if other.Collection(c) == nil {
				return registry.Invalid("deck: reads: project %q has no collection %q", r.Project, c)
			}
		}
	}
	return nil
}

// unassigned are the warnings of the model commands of a manifest that no model of the settings takes.
func (pj *Service) unassigned(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	var out []string
	for name, text := range pj.Unassigned(ctx, p.WorkspaceID, p.ID, m) {
		out = append(out, "command "+name+": "+text)
	}
	slices.Sort(out)
	return out
}

// unpaged are the blocks without a limit on a collection that holds more records than a page: each shows a page and
// more by a button.
func (pj *Service) unpaged(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	counts, err := pj.St.Q.CountRecords(ctx, p.ID)
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

// heldKeys are the problems of a manifest whose tools or MCP servers get the key of a connection: a module reaches
// models only through deck, by a model of its settings.
func heldKeys(ctx context.Context, q *db.Queries, workspace int64, m *manifest.Manifest) ([]string, error) {
	conns, err := q.ListConnections(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range conns {
		if c.KeySecret != "" && slices.Contains(m.Secrets(), c.KeySecret) {
			out = append(out, fmt.Sprintf("secret %s is the key of connection %s: a module calls models through deck, with deck.model_call by kind or $DECK_MODELS and a model of deck's settings, not with the key", c.KeySecret, c.Name))
		}
	}
	return out, nil
}

// checkKeys refuses a manifest whose tools or MCP servers get the key of a connection.
func checkKeys(ctx context.Context, q *db.Queries, workspace int64, m *manifest.Manifest) error {
	held, err := heldKeys(ctx, q, workspace, m)
	if err != nil || len(held) == 0 {
		return err
	}
	return registry.Invalid("deck: %s", strings.Join(held, "\n"))
}

// checkToolFit checks tool commands whose tool is a builtin or an MCP tool deck has listed: the command
// input must go to the tool as it is. An MCP tool deck has not seen yet is checked when the command runs.
func (pj *Service) checkToolFit(ctx context.Context, q *db.Queries, workspace, projectID int64, m *manifest.Manifest) error {
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
		if strings.HasPrefix(name, registry.Builtin+".") {
			t, err := pj.Registry.Lookup(ctx, workspace, name)
			if err != nil {
				return registry.Invalid("deck: command %q: no builtin tool %q", c.Name, name)
			}
			input = t.Input
		} else if server, tool, ok := strings.Cut(name, "."); ok {
			s := m.Tools.MCP.Servers[server]
			i := slices.IndexFunc(listed, func(r db.McpTool) bool {
				return r.Server == server && r.Name == tool && r.Listed == 1 && r.Spec == tools.SpecID(s)
			})
			if i < 0 {
				continue
			}
			input = json.RawMessage(listed[i].Input)
		} else {
			continue
		}
		if err := manifest.FitsTool(c, name, input); err != nil {
			return registry.Invalid("deck: command %q: %v", c.Name, err)
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
		return registry.Invalid("deck: %s", strings.Join(msgs, "\n"))
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

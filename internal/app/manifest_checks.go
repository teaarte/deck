package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/tools"
)

const maxProblems = 20

// checkReads verifies the projects and collections a manifest reads.
func checkReads(ctx context.Context, q *db.Queries, workspace int64, name string, m *manifest.Manifest) error {
	for _, r := range m.Reads {
		if r.Project == name {
			return registry.Invalid("deck: reads: a project does not need to read itself")
		}
		p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: r.Project})
		if err != nil {
			return registry.Invalid("deck: reads: no project %q", r.Project)
		}
		if p.Builtin == 1 {
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

// checkToolFit checks tool commands whose tool is a builtin or an MCP tool deck has listed: the command
// input must go to the tool as it is. An MCP tool deck has not seen yet is checked when the command runs.
func (a *App) checkToolFit(ctx context.Context, q *db.Queries, workspace, projectID int64, m *manifest.Manifest) error {
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
			t, err := a.Registry.Lookup(ctx, workspace, name)
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

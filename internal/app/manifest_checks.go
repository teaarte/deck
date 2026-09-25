package app

import (
	"bytes"
	"context"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

const maxProblems = 20

// checkReads verifies the projects and collections a manifest reads.
func checkReads(ctx context.Context, q *db.Queries, workspace int64, name string, m *manifest.Manifest) error {
	for _, r := range m.Reads {
		if r.Project == name {
			return registry.Invalid("deck: reads: a project does not need to read itself")
		}
		p, err := q.GetProjectByName(ctx, r.Project)
		if err != nil || p.WorkspaceID != workspace {
			return registry.Invalid("deck: reads: no project %q", r.Project)
		}
		other, err := loadManifest(ctx, q, p)
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

// checkRecords refuses a manifest whose collections no longer fit the stored records.
func checkRecords(ctx context.Context, q *db.Queries, p db.Project, next *manifest.Manifest) error {
	counts, err := q.CountRecords(ctx, p.ID)
	if err != nil {
		return err
	}
	old, err := loadManifest(ctx, q, p)
	if err != nil {
		slog.Warn("current manifest does not parse; checking every record", "project", p.Name, "err", err)
		old = nil
	}
	var msgs []string
	for _, cnt := range counts {
		c := next.Collection(cnt.Collection)
		if c == nil {
			msgs = append(msgs, fmt.Sprintf("collection %q is removed but has %d records; delete them first with deck.collection_delete", cnt.Collection, cnt.N))
			continue
		}
		if old != nil && sameShape(old.Collection(cnt.Collection), c) {
			continue
		}
		msg, err := misfits(ctx, q, p.ID, c, cnt.N)
		if err != nil {
			return err
		}
		if msg != "" {
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

// misfits describes the records of a collection that the new schema or key would break; "" when all fit.
func misfits(ctx context.Context, q *db.Queries, projectID int64, c *manifest.Collection, total int64) (string, error) {
	recs, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: projectID, Collection: c.Name})
	if err != nil {
		return "", err
	}
	var problems []string
	bad := 0
	keys := map[string]int64{}
	for _, r := range recs {
		var why string
		data, err := manifest.DecodeObject([]byte(r.Data))
		switch {
		case err != nil:
			why = "stored data is not a JSON object"
		case c.Validate(data) != nil:
			why = c.Validate(data).Error()
		default:
			key, err := c.KeyOf(data)
			switch {
			case err != nil:
				why = err.Error()
			case key != r.Key:
				why = fmt.Sprintf("key would change from %q to %q", r.Key, key)
			case keys[key] != 0:
				why = fmt.Sprintf("key %q collides with record %d", key, keys[key])
			default:
				keys[key] = r.ID
			}
		}
		if why == "" {
			continue
		}
		bad++
		if len(problems) < maxProblems {
			problems = append(problems, fmt.Sprintf("  - record %d (key %q): %s", r.ID, r.Key, why))
		}
	}
	if bad == 0 {
		return "", nil
	}
	fix := "add a migration script to this revision"
	if c.Migration != "" {
		fix = "migration scripts are not available yet"
	}
	return fmt.Sprintf("collection %q: %d of %d records do not fit the new schema or key; %s. First problems:\n%s",
		c.Name, bad, total, fix, strings.Join(problems, "\n")), nil
}

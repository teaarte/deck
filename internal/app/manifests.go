package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const maxProblems = 20

// checkManifest validates a manifest against the rest of deck: projects it reads and the records it keeps.
func (a *App) checkManifest(ctx context.Context, projectID int64, name string, next *manifest.Manifest) error {
	for _, r := range next.Reads {
		if r.Project == name {
			return registry.Invalid("deck: reads: a project does not need to read itself")
		}
		p, err := a.Store.Q.GetProjectByName(ctx, r.Project)
		if err != nil {
			return registry.Invalid("deck: reads: no project %q", r.Project)
		}
		other, err := a.Manifest(ctx, p.ID)
		if err != nil {
			return registry.Invalid("deck: reads: project %q has an invalid manifest: %v", r.Project, err)
		}
		for _, c := range r.Collections {
			if other.Collection(c) == nil {
				return registry.Invalid("deck: reads: project %q has no collection %q", r.Project, c)
			}
		}
	}
	if projectID == 0 {
		return nil
	}
	return a.checkRecords(ctx, projectID, next)
}

// checkRecords refuses a manifest whose collections no longer fit the stored records.
func (a *App) checkRecords(ctx context.Context, projectID int64, next *manifest.Manifest) error {
	counts, err := a.Store.Q.CountRecords(ctx, projectID)
	if err != nil {
		return err
	}
	old, err := a.Manifest(ctx, projectID)
	if err != nil {
		slog.Warn("current manifest does not parse; checking every record", "project", projectID, "err", err)
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
		if msg, err := a.misfits(ctx, projectID, c, cnt.N); err != nil {
			return err
		} else if msg != "" {
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
func (a *App) misfits(ctx context.Context, projectID int64, c *manifest.Collection, total int64) (string, error) {
	recs, err := a.Store.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: projectID, Collection: c.Name})
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

// UpgradeManifests raises stored manifests of an older format to the current one as new revisions.
func (a *App) UpgradeManifests(ctx context.Context) error {
	ps, err := a.Store.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		raw, err := a.DocumentText(ctx, p.ID, KindManifest, ManifestName)
		if err != nil {
			return err
		}
		up, changed, err := manifest.Upgrade([]byte(raw))
		if err == nil && changed {
			_, err = manifest.Parse(up)
		}
		if err != nil || !changed {
			if err != nil {
				slog.Error("upgrade manifest; keeping the stored revision", "project", p.Name, "err", err)
			}
			continue
		}
		err = a.Store.Tx(ctx, func(q *db.Queries) error {
			_, _, err := a.SaveDocument(ctx, q, DocRef{ProjectID: p.ID, Name: ManifestName}, KindManifest, string(up), "deck",
				fmt.Sprintf("manifest format upgraded to version %d", manifest.FormatVersion))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) collectionDelete(ctx context.Context, act registry.Actor, in struct{ Project, Collection string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	n, err := a.Store.Q.DeleteCollection(ctx, db.DeleteCollectionParams{ProjectID: p.ID, Collection: in.Collection})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("collection")
	return map[string]any{"deleted": n}, nil
}

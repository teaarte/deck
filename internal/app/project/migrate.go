package project

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/script"
	"deck2/internal/store/db"
)

// envelope is one record as a migration script sees it; a record without id is new.
type envelope struct {
	ID   int64           `json:"id,omitempty"`
	Data json.RawMessage `json:"data"`
}

// migration is the new content of a collection and the records it was made from.
type migration struct {
	input string
	rows  []envelope
	// checked is a migration a check of the manifest names without running its script.
	checked bool
}

func envelopes(recs []db.Record) string {
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "{\"id\":%d,\"data\":%s}\n", r.ID, r.Data)
	}
	return b.String()
}

func parseEnvelopes(jsonl string) ([]envelope, error) {
	var out []envelope
	sc := bufio.NewScanner(strings.NewReader(jsonl))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e envelope
		if err := json.Unmarshal([]byte(line), &e); err != nil || len(e.Data) == 0 {
			return nil, fmt.Errorf("line %d is not {\"id\": <record id or none>, \"data\": {...}}", n)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// migrate prepares, outside any transaction, the collections whose schema or key changes: from the snapshot
// a revert returns to, or by the migration script when the records no longer fit; a check runs no script.
func (pj *Service) migrate(ctx context.Context, p db.Project, next *manifest.Manifest, revertTo int64, check bool) (map[string]*migration, error) {
	old, err := docs.Manifest(ctx, pj.St.Q, p)
	if err != nil {
		return nil, nil
	}
	out := map[string]*migration{}
	for i := range next.Collections {
		c := &next.Collections[i]
		if oc := old.Collection(c.Name); oc == nil || sameShape(oc, c) {
			continue
		}
		recs, err := pj.St.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: c.Name})
		if err != nil {
			return nil, err
		}
		input := envelopes(recs)
		if revertTo > 0 {
			snap, err := pj.St.Q.FindRecordSnapshot(ctx, db.FindRecordSnapshotParams{ProjectID: p.ID, Collection: c.Name, Revision: revertTo})
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			rows, err := parseEnvelopes(snap.Records)
			if err != nil {
				return nil, err
			}
			out[c.Name] = &migration{input: input, rows: rows}
			continue
		}
		if len(recs) == 0 || c.Migration == "" || misfitsOf(c, storedRows(recs), true, "") == "" {
			continue
		}
		if check {
			out[c.Name] = &migration{input: input, checked: true}
			continue
		}
		stdout, err := pj.runMigration(ctx, p, next, c, input)
		if err != nil {
			return nil, registry.Invalid("deck: migration script %s of collection %q failed: %v", c.Migration, c.Name, err)
		}
		rows, err := parseEnvelopes(string(stdout))
		if err != nil {
			return nil, registry.Invalid("deck: migration script %s of collection %q: %v", c.Migration, c.Name, err)
		}
		ids := map[int64]bool{}
		for _, r := range recs {
			ids[r.ID] = true
		}
		for _, r := range rows {
			if r.ID != 0 && !ids[r.ID] {
				return nil, registry.Invalid("deck: migration script %s returned record id %d that was not in its input or twice", c.Migration, r.ID)
			}
			delete(ids, r.ID)
		}
		out[c.Name] = &migration{input: input, rows: rows}
	}
	return out, nil
}

// runMigration runs a migration script on a copy of the records, without network and deck tools.
func (pj *Service) runMigration(ctx context.Context, p db.Project, m *manifest.Manifest, c *manifest.Collection, input string) ([]byte, error) {
	s := m.Script(c.Migration)
	src, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindScript, s.File)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(src) == "" {
		return nil, fmt.Errorf("script file %q is missing or empty", s.File)
	}
	limit := process.ScriptCeiling
	if s.TimeoutMinutes > 0 {
		limit = time.Duration(s.TimeoutMinutes) * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	dir := filepath.Join(pj.Home.Runs(), "migration-"+uuid.NewString())
	defer os.RemoveAll(dir)
	out, err := pj.Scripts.Run(ctx, script.Job{Project: p.ID, Script: s, Source: src, Dir: dir, Stdin: []byte(input)})
	if ctx.Err() != nil {
		return nil, fmt.Errorf("it ran longer than %s", limit)
	}
	return out, err
}

// applyMigration replaces the records of a collection with the migrated ones, keeping ids, and stores the
// records it replaced as a snapshot of the revision the manifest leaves.
func applyMigration(ctx context.Context, q *db.Queries, now string, projectID int64, c *manifest.Collection, mg *migration, from, to int64) error {
	recs, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: projectID, Collection: c.Name})
	if err != nil {
		return err
	}
	if envelopes(recs) != mg.input {
		return registry.Invalid("deck: records of %q changed while the migration ran; save the manifest again", c.Name)
	}
	if err := q.CreateRecordSnapshot(ctx, db.CreateRecordSnapshotParams{ProjectID: projectID, Collection: c.Name,
		FromRevision: from, Revision: to, Records: mg.input}); err != nil {
		return err
	}
	existing := map[int64]bool{}
	for _, r := range recs {
		existing[r.ID] = true
		if err := q.ParkRecordKey(ctx, r.ID); err != nil {
			return err
		}
	}
	kept := map[int64]bool{}
	for _, r := range mg.rows {
		kept[r.ID] = true
	}
	for _, r := range recs {
		if !kept[r.ID] {
			if err := q.DeleteRecord(ctx, r.ID); err != nil {
				return err
			}
		}
	}
	for _, r := range mg.rows {
		data, err := manifest.DecodeObject(r.Data)
		if err != nil {
			return err
		}
		key, err := c.KeyOf(data)
		if err != nil {
			return err
		}
		b := manifest.JSON(data, "")
		if existing[r.ID] {
			err = q.PutRecord(ctx, db.PutRecordParams{Key: key, Data: string(b), Now: now, ID: r.ID})
		} else {
			_, err = q.InsertRecord(ctx, db.InsertRecordParams{ID: sql.NullInt64{Int64: r.ID, Valid: r.ID != 0},
				ProjectID: projectID, Collection: c.Name, Key: key, Data: string(b), Now: now})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

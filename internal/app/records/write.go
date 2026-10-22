package records

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type UpsertResult struct {
	Written int `json:"written"`
	// IDs are the ids of the written records, in order.
	IDs    []int64  `json:"ids"`
	Errors []string `json:"errors,omitempty"`
}

// UpsertJSONL writes one record per line; records with an existing key are updated. A new record keeps the process
// that wrote it, when one did.
func UpsertJSONL(ctx context.Context, q *db.Queries, now time.Time, projectID, process int64, m *manifest.Manifest, collection, jsonl string) (UpsertResult, error) {
	var recs []map[string]any
	var out UpsertResult
	sc := bufio.NewScanner(strings.NewReader(jsonl))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		r, err := manifest.DecodeObject([]byte(text))
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return out, registry.Invalid("deck: records: %v", err)
	}
	res, err := upsert(ctx, q, now, projectID, process, m, collection, recs)
	res.Errors = append(out.Errors, res.Errors...)
	return res, err
}

// upsert validates each record against the collection schema and writes it by its key; run it in
// a transaction so the immutable-field check and the write see the same record.
func upsert(ctx context.Context, q *db.Queries, now time.Time, projectID, process int64, m *manifest.Manifest, collection string, recs []map[string]any) (UpsertResult, error) {
	c := m.Collection(collection)
	if c == nil {
		return UpsertResult{}, registry.Invalid("deck: unknown collection %q", collection)
	}
	var out UpsertResult
	err := func() error {
		for i, r := range recs {
			key, err := c.RecordKey(r)
			if err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
				continue
			}
			prev, err := q.GetRecordByKey(ctx, db.GetRecordByKeyParams{ProjectID: projectID, Collection: collection, Key: key})
			switch {
			case err == nil:
				old, derr := manifest.DecodeObject([]byte(prev.Data))
				if derr == nil {
					if err := c.CheckImmutable(old, r); err != nil {
						out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
						continue
					}
				}
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
			data := manifest.JSON(r, "")
			rec, err := q.UpsertRecord(ctx, db.UpsertRecordParams{ProjectID: projectID, Collection: collection, Key: key, Data: string(data), Now: store.Time(now),
				ProcessID: store.ID(process)})
			if err != nil {
				return err
			}
			out.Written++
			out.IDs = append(out.IDs, rec.ID)
		}
		return nil
	}()
	if err != nil {
		return UpsertResult{}, err
	}
	return out, nil
}

// Update replaces the data of a record; the key and immutable fields must stay the same.
func Update(ctx context.Context, q *db.Queries, now time.Time, rec db.Record, m *manifest.Manifest, data map[string]any) error {
	c := m.Collection(rec.Collection)
	if c == nil {
		return registry.Invalid("deck: collection %q no longer exists", rec.Collection)
	}
	key, err := c.RecordKey(data)
	if err != nil {
		return registry.Invalid("deck: %v", err)
	}
	if key != rec.Key {
		return registry.Invalid("deck: the change would move the record key from %q to %q; key fields cannot change", rec.Key, key)
	}
	if old, err := manifest.DecodeObject([]byte(rec.Data)); err == nil {
		if err := c.CheckImmutable(old, data); err != nil {
			return registry.Invalid("deck: %v", err)
		}
	}
	b := manifest.JSON(data, "")
	n, err := q.UpdateRecordData(ctx, db.UpdateRecordDataParams{Data: string(b), Now: store.Time(now), ID: rec.ID, ProjectID: rec.ProjectID, Collection: rec.Collection, Key: rec.Key})
	if err != nil {
		return err
	}
	if n == 0 {
		return registry.NotFound("deck: record %d no longer exists", rec.ID)
	}
	return nil
}

// writeRecords runs a record write in one transaction with the manifest it validates against, so a manifest
// save cannot change the schema between the check and the write.
func (s *Service) writeRecords(ctx context.Context, p db.Project, f func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return f(ctx, q, m)
	})
}

type recordsUpsertIn struct {
	Project    string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Collection string `desc:"Collection name"`
	Records    string `type:"text" desc:"Records as JSONL: one JSON object per line"`
}

func (s *Service) recordsUpsert(ctx context.Context, act registry.Actor, in recordsUpsertIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var out UpsertResult
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		out, err = UpsertJSONL(ctx, q, s.Now(), p.ID, act.ProcessID, m, in.Collection, in.Records)
		return err
	})
	return out, err
}

type recordUpdateIn struct {
	Record int64  `type:"record" desc:"Record id"`
	Data   string `type:"json" desc:"New record JSON"`
}

func (s *Service) recordUpdate(ctx context.Context, act registry.Actor, in recordUpdateIn) (any, error) {
	data, err := manifest.DecodeObject([]byte(in.Data))
	if err != nil {
		return nil, registry.Invalid("deck: data is not a JSON object: %v", err)
	}
	rec, err := s.St.Q.GetRecord(ctx, in.Record)
	rec, p, err := access.Owned(ctx, s.St.Q, act, rec, err, rec.ProjectID, registry.NotFound("deck: no record %d", in.Record))
	if err != nil {
		return nil, err
	}
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		cur, err := q.GetRecord(ctx, rec.ID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && cur.ProjectID != p.ID {
			return registry.NotFound("deck: record %d no longer exists", rec.ID)
		}
		if err != nil {
			return err
		}
		return Update(ctx, q, s.Now(), cur, m, data)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

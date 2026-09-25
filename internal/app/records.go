package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type UpsertResult struct {
	Written int      `json:"written"`
	Errors  []string `json:"errors,omitempty"`
}

// UpsertJSONL writes one record per line; records with an existing key are updated.
func (a *App) UpsertJSONL(ctx context.Context, projectID int64, m *manifest.Manifest, collection, jsonl string) (UpsertResult, error) {
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
			out.Errors = append(out.Errors, fmt.Sprintf("line %d: not a JSON object: %v", line, err))
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return out, registry.Invalid("deck: records: %v", err)
	}
	res, err := a.UpsertRecords(ctx, projectID, m, collection, recs)
	res.Errors = append(out.Errors, res.Errors...)
	return res, err
}

// UpsertRecords validates each record against the collection schema and writes it by its key.
func (a *App) UpsertRecords(ctx context.Context, projectID int64, m *manifest.Manifest, collection string, recs []map[string]any) (UpsertResult, error) {
	c := m.Collection(collection)
	if c == nil {
		return UpsertResult{}, registry.Invalid("deck: unknown collection %q", collection)
	}
	var out UpsertResult
	for i, r := range recs {
		key, err := c.RecordKey(r)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
			continue
		}
		if prev, err := a.Store.Q.GetRecordByKey(ctx, db.GetRecordByKeyParams{ProjectID: projectID, Collection: collection, Key: key}); err == nil {
			if old, err := manifest.DecodeObject([]byte(prev.Data)); err == nil {
				if err := c.CheckImmutable(old, r); err != nil {
					out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
					continue
				}
			}
		}
		data, _ := json.Marshal(r)
		if _, err := a.Store.Q.UpsertRecord(ctx, db.UpsertRecordParams{ProjectID: projectID, Collection: collection, Key: key, Data: string(data)}); err != nil {
			return out, err
		}
		out.Written++
	}
	if out.Written > 0 {
		a.Bus.Publish("collection")
	}
	return out, nil
}

// UpdateRecord replaces the data of a record; the key must stay the same.
func (a *App) UpdateRecord(ctx context.Context, rec db.Record, m *manifest.Manifest, data map[string]any) error {
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
	b, _ := json.Marshal(data)
	if err := a.Store.Q.UpdateRecordData(ctx, db.UpdateRecordDataParams{Data: string(b), ID: rec.ID}); err != nil {
		return err
	}
	a.Bus.Publish("collection")
	return nil
}

type RecordView struct {
	ID         int64           `json:"id"`
	Collection string          `json:"collection"`
	Key        string          `json:"key"`
	Title      string          `json:"title,omitempty"`
	Data       json.RawMessage `json:"data"`
	UpdatedAt  string          `json:"updated_at"`
}

func recordView(r db.Record, c *manifest.Collection) RecordView {
	v := RecordView{ID: r.ID, Collection: r.Collection, Key: r.Key, Data: json.RawMessage(r.Data), UpdatedAt: r.UpdatedAt}
	if c != nil {
		if data, err := manifest.DecodeObject([]byte(r.Data)); err == nil {
			v.Title = c.RecordTitle(data)
		}
	}
	return v
}

package screen

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

// items are the items of a source as expression environments, filtered, sorted and cut.
func (r *renderer) items(src manifest.Source, picked map[string]string) ([]map[string]any, error) {
	var items []map[string]any
	switch src.From {
	case "", "collection":
		project, err := r.sourceProject(src)
		if err != nil {
			return nil, err
		}
		recs, err := r.st.Q.ListRecords(r.ctx, db.ListRecordsParams{ProjectID: project, Collection: src.Collection})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			env, err := manifest.DecodeObject([]byte(rec.Data))
			if err != nil {
				continue
			}
			env["_id"], env["_updated"] = rec.ID, rec.UpdatedAt
			items = append(items, env)
		}
	default:
		scope := r.scope
		scope.Filter = picked
		if src.From == "log" {
			scope.Record = r.req.Record
		}
		got, err := r.src.Items(r.ctx, src.From, scope)
		if err != nil {
			return nil, err
		}
		for _, it := range got {
			if _, ok := it["_id"]; !ok {
				it["_id"] = it["id"]
			}
			items = append(items, it)
		}
	}
	if src.Where != "" {
		kept := items[:0]
		for _, it := range items {
			env := it
			if r.record != nil {
				env = withRecord(it, r.record)
			}
			ok, err := manifest.Eval(src.Where, env)
			if err != nil {
				return nil, fmt.Errorf("where: %w", err)
			}
			if ok == true {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	return items, nil
}

// filtered are the items of a source narrowed by the values the owner picked, then sorted and cut; each
// filter offers the values its field has among the items the where keeps.
func (r *renderer) filtered(src manifest.Source, fields []string, picked map[string]string) ([]map[string]any, []map[string]any, error) {
	items, err := r.items(src, nil)
	if err != nil {
		return nil, nil, err
	}
	filters := make([]map[string]any, 0, len(fields))
	chosen := map[string]string{}
	for _, f := range fields {
		seen := map[string]bool{}
		for _, it := range items {
			if v := text(it[f]); v != "" {
				seen[v] = true
			}
		}
		fl := map[string]any{"field": f}
		if v := picked[f]; v != "" {
			fl["value"], seen[v], chosen[f] = v, true, v
		}
		fl["options"] = append([]string{}, slices.Sorted(maps.Keys(seen))...)
		filters = append(filters, fl)
	}
	if len(chosen) > 0 && src.From != "" && src.From != "collection" {
		if items, err = r.items(src, chosen); err != nil {
			return nil, nil, err
		}
	}
	for f, v := range chosen {
		kept := items[:0:0]
		for _, it := range items {
			if text(it[f]) == v {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	return sortAndCut(items, src), filters, nil
}

func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func sortAndCut(items []map[string]any, src manifest.Source) []map[string]any {
	if s := src.Sort; s != "" {
		field, desc := strings.TrimPrefix(s, "-"), strings.HasPrefix(s, "-")
		sort.SliceStable(items, func(i, j int) bool {
			if desc {
				return less(items[j][field], items[i][field])
			}
			return less(items[i][field], items[j][field])
		})
	}
	if src.Limit > 0 && len(items) > src.Limit {
		items = items[:src.Limit]
	}
	return items
}

func withRecord(env, record map[string]any) map[string]any {
	out := make(map[string]any, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["record"] = record
	return out
}

// sourceProject is the project a block reads: its own, or another one its manifest reads.
func (r *renderer) sourceProject(src manifest.Source) (int64, error) {
	if src.Project == "" {
		return r.req.Project.ID, nil
	}
	other, err := r.st.Q.GetProjectByName(r.ctx, db.GetProjectByNameParams{WorkspaceID: r.req.Project.WorkspaceID, Name: src.Project})
	if err != nil {
		return 0, fmt.Errorf("no project %q", src.Project)
	}
	for _, rd := range r.req.Manifest.Reads {
		if rd.Project == src.Project && slices.Contains(rd.Collections, src.Collection) {
			return other.ID, nil
		}
	}
	return 0, fmt.Errorf("%s.%s is not in reads", src.Project, src.Collection)
}

func less(a, b any) bool {
	fa, aok := number(a)
	fb, bok := number(b)
	if aok && bok {
		return fa < fb
	}
	return fmt.Sprint(a) < fmt.Sprint(b)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

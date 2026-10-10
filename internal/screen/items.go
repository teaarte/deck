package screen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"deck2/internal/manifest"
)

// items are the items of a source as expression environments, kept by its where.
func (r *renderer) items(src manifest.Source, picked map[string]string) ([]map[string]any, error) {
	items, err := r.fetch(src, picked)
	if err != nil {
		return nil, err
	}
	if src.Where != "" {
		kept := items[:0]
		for _, it := range items {
			env := it
			if r.record != nil {
				env = withRecord(it, r.record)
			}
			ok, err := r.eval(src.Where, env)
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

// fetch reads the items of a source once per screen for what it reads: the collection, or the command with its
// input and the values picked; each caller gets its own list of the shared items.
func (r *renderer) fetch(src manifest.Source, picked map[string]string) ([]map[string]any, error) {
	input, err := r.input(src.Input)
	if err != nil {
		return nil, err
	}
	k, _ := json.Marshal([]any{src.From, src.Project, src.Collection, src.Rows, input, picked})
	if got, ok := r.fetched[string(k)]; ok {
		return slices.Clone(got.items), got.err
	}
	items, err := r.load(src, input, picked)
	r.fetched[string(k)] = fetchOf{items, err}
	return slices.Clone(items), err
}

type fetchOf struct {
	items []map[string]any
	err   error
}

func (r *renderer) load(src manifest.Source, input map[string]any, picked map[string]string) ([]map[string]any, error) {
	var items []map[string]any
	switch src.From {
	case "", "collection":
		scope, project := r.scope, r.req.Project.Name
		if src.Project != "" {
			scope.By, project = r.req.Project.ID, src.Project
		}
		got, err := r.src.Read(r.ctx, "deck.records_list", map[string]any{"project": project, "collection": src.Collection}, scope)
		if err != nil {
			return nil, err
		}
		list, _ := got.([]any)
		for _, v := range list {
			rec, _ := v.(map[string]any)
			env, _ := rec["data"].(map[string]any)
			if env == nil {
				continue
			}
			env["_id"], env["_updated"] = rec["id"], rec["updated_at"]
			items = append(items, env)
		}
	default:
		scope := r.scope
		scope.Filter = picked
		if other, _ := input["project"].(string); !r.deck() && other != "" && other != r.req.Project.Name {
			scope.By = r.req.Project.ID
		}
		got, err := r.src.Read(r.ctx, src.From, input, scope)
		if err != nil {
			return nil, err
		}
		if src.Rows != "" {
			obj, _ := got.(map[string]any)
			got = obj[src.Rows]
		}
		list, ok := got.([]any)
		if !ok {
			return nil, fmt.Errorf("%s gives no list", src.From)
		}
		for i, v := range list {
			it, ok := v.(map[string]any)
			if !ok {
				continue
			}
			it["_id"] = it["id"]
			if it["_id"] == nil {
				it["_id"] = cmp.Or(it["name"], any(int64(i+1)))
			}
			items = append(items, it)
		}
	}
	return items, nil
}

// input is the input of a command a block reads: its values resolved over the record of the page and the
// parameters of the screen; a value that fails or resolves to nothing is an error, not a read without it.
func (r *renderer) input(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok && s != "" {
			var err error
			if v, err = manifest.Resolve(s, "", r.withParam(r.record)); err != nil {
				return nil, fmt.Errorf("input %s: %w", k, err)
			}
			if v == nil {
				return nil, fmt.Errorf("input %s: %s gives nothing", k, s)
			}
		}
		out[k] = v
	}
	return out, nil
}

// filtered are the items of a source narrowed by the values the owner picked, then sorted and cut, with how many
// the cut left out; each filter offers the values its field has among the items the where keeps.
func (r *renderer) filtered(src manifest.Source, fields []string, picked map[string]string) ([]map[string]any, []map[string]any, int, error) {
	items, err := r.items(src, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	filters := make([]map[string]any, 0, len(fields))
	chosen := map[string]string{}
	for _, f := range fields {
		seen := map[string]bool{}
		for _, it := range items {
			for _, v := range values(it[f]) {
				seen[v] = true
			}
		}
		fl := map[string]any{"field": f}
		if l := r.tr(f); l != f {
			fl["label"] = l
		}
		if v := picked[f]; v != "" {
			fl["value"], seen[v], chosen[f] = v, true, v
		}
		options := slices.Sorted(maps.Keys(seen))
		fl["options"] = append([]string{}, options...)
		labels, named := make([]string, len(options)), false
		for i, o := range options {
			labels[i] = fmt.Sprint(r.word(src.Collection, f, o))
			named = named || labels[i] != o
		}
		if named {
			fl["labels"] = labels
		}
		filters = append(filters, fl)
	}
	if len(chosen) > 0 && src.From != "" && src.From != "collection" {
		if items, err = r.items(src, chosen); err != nil {
			return nil, nil, 0, err
		}
	}
	for f, v := range chosen {
		kept := items[:0:0]
		for _, it := range items {
			if slices.Contains(values(it[f]), v) {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	items, more := sortAndCut(items, src)
	return items, filters, more, nil
}

// values are what a filter of a field offers of an item: each value of a list, else the value itself.
func values(v any) []string {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, x := range list {
			if t := text(x); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	if t := text(v); t != "" {
		return []string{t}
	}
	return nil
}

func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func sortAndCut(items []map[string]any, src manifest.Source) ([]map[string]any, int) {
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
		return items[:src.Limit], len(items) - src.Limit
	}
	return items, 0
}

func withRecord(env, record map[string]any) map[string]any {
	out := make(map[string]any, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["record"] = record
	return out
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

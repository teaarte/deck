package screen

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	Version   = "v0.9"
	CatalogID = "https://deck2.local/catalog/v1"
)

// Render resolves a stored screen into A2UI messages: createSurface, updateComponents, updateDataModel.
// Clients only draw components and send actions back.
func Render(ctx context.Context, st *store.Store, projectID int64, m *manifest.Manifest, name string) ([]map[string]any, error) {
	sc := m.Screen(name)
	if sc == nil {
		return nil, fmt.Errorf("unknown screen %q", name)
	}
	surface := fmt.Sprintf("p%d-%s", projectID, name)
	root := map[string]any{"id": "root", "component": "Column", "children": []string{}}
	components := []map[string]any{root}
	data := map[string]any{}
	children := []string{}

	for i, b := range sc.Blocks {
		id := fmt.Sprintf("b%d", i)
		children = append(children, id)
		switch b.Type {
		case "table":
			rows, err := resolve(ctx, st, projectID, b)
			if err != nil {
				components = append(components, map[string]any{"id": id, "component": "Text", "text": "source error: " + err.Error(), "variant": "error"})
				continue
			}
			data[id] = rows
			cols := make([]map[string]any, 0, len(b.Columns))
			for j, c := range b.Columns {
				cols = append(cols, map[string]any{"label": c.Label, "value": map[string]any{"path": fmt.Sprintf("c%d", j)}})
			}
			comp := map[string]any{"id": id, "component": "Table", "title": b.Title, "rows": map[string]any{"path": "/" + id}, "columns": cols}
			if len(b.Actions) > 0 {
				comp["rowActions"] = actions(b.Actions)
			}
			components = append(components, comp)
		case "actions":
			components = append(components, map[string]any{"id": id, "component": "Actions", "title": b.Title, "actions": actions(b.Actions)})
		}
	}
	root["children"] = children
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surface, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surface, "components": components}},
		{"version": Version, "updateDataModel": map[string]any{"surfaceId": surface, "value": data}},
	}, nil
}

func actions(as []manifest.Action) []map[string]any {
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{"label": a.Label,
			"action": map[string]any{"event": map[string]any{"name": "command", "context": map[string]any{"command": a.Command, "params": a.Params}}}})
	}
	return out
}

func resolve(ctx context.Context, st *store.Store, projectID int64, b manifest.Block) ([]map[string]any, error) {
	recs, err := st.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: projectID, Collection: b.Source.Collection})
	if err != nil {
		return nil, err
	}
	type row struct {
		env map[string]any
		out map[string]any
	}
	rows := make([]row, 0, len(recs))
	for _, r := range recs {
		env := map[string]any{}
		if err := json.Unmarshal([]byte(r.Data), &env); err != nil {
			continue
		}
		env["_id"] = r.ID
		env["_updated"] = r.UpdatedAt
		if b.Source.Where != "" {
			ok, err := manifest.Eval(b.Source.Where, env)
			if err != nil {
				return nil, fmt.Errorf("where: %w", err)
			}
			if ok != true {
				continue
			}
		}
		out := map[string]any{"id": r.ID}
		for j, c := range b.Columns {
			v, err := manifest.Eval(c.Bind, env)
			if err != nil {
				return nil, fmt.Errorf("column %q: %w", c.Label, err)
			}
			out[fmt.Sprintf("c%d", j)] = v
		}
		rows = append(rows, row{env: env, out: out})
	}
	if s := b.Source.Sort; s != "" {
		field, desc := strings.TrimPrefix(s, "-"), strings.HasPrefix(s, "-")
		sort.SliceStable(rows, func(i, j int) bool {
			less := fmt.Sprint(rows[i].env[field]) < fmt.Sprint(rows[j].env[field])
			if desc {
				return !less && fmt.Sprint(rows[i].env[field]) != fmt.Sprint(rows[j].env[field])
			}
			return less
		})
	}
	if b.Source.Limit > 0 && len(rows) > b.Source.Limit {
		rows = rows[:b.Source.Limit]
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.out)
	}
	return out, nil
}

package scenarios

import "encoding/json"

type screenRow struct {
	ID      any              `json:"id"`
	Cells   []any            `json:"cells"`
	Actions []map[string]any `json:"actions"`
}

// screenMessages resolves a screen of a project through deck.screen.
func (e *Env) screenMessages(project, screen string, extra map[string]any) []map[string]json.RawMessage {
	e.T.Helper()
	in := map[string]any{"screen": screen}
	if project != "" {
		in["project"] = project
	}
	for k, v := range extra {
		in[k] = v
	}
	var msgs []map[string]json.RawMessage
	e.must("deck.screen", in, &msgs)
	return msgs
}

// screenData is the data model of a resolved screen, by block id.
func (e *Env) screenData(project, screen string, extra map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	for _, m := range e.screenMessages(project, screen, extra) {
		if raw, ok := m["updateDataModel"]; ok {
			var dm struct {
				Value map[string]map[string]json.RawMessage `json:"value"`
			}
			_ = json.Unmarshal(raw, &dm)
			return dm.Value
		}
	}
	return nil
}

// screenRows returns the rows of the first table block of a screen.
func (e *Env) screenRows(project int64, screen string) []screenRow {
	e.T.Helper()
	var rows []screenRow
	_ = json.Unmarshal(e.screenData(e.names[project], screen, nil)["b0"]["rows"], &rows)
	return rows
}

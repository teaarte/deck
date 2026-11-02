package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pressly/goose/v3"
)

// format16 raises the stored manifests and those of open asks to format 16, where a command runs one at a time unless
// it says runs many: a command without runs whose real runs went side by side gets runs many.
var format16 = goose.NewGoMigration(58, &goose.GoFunc{RunTx: toFormat16}, nil)

// sideBySide are the commands of each project, by project id, whose real runs overlapped: one started before an
// earlier one of the same command ended.
const sideBySide = `WITH w AS (
	SELECT project_id, command, started_at, max(COALESCE(finished_at, '9999')) OVER (PARTITION BY project_id, command
		ORDER BY started_at, id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) AS before_end
	FROM process WHERE trial = 0)
SELECT DISTINCT project_id, command FROM w WHERE started_at < before_end`

func toFormat16(ctx context.Context, tx *sql.Tx) error {
	many := map[int64]map[string]bool{}
	rows, err := tx.QueryContext(ctx, sideBySide)
	if err != nil {
		return err
	}
	for rows.Next() {
		var project int64
		var command string
		if err := rows.Scan(&project, &command); err != nil {
			_ = rows.Close()
			return err
		}
		if many[project] == nil {
			many[project] = map[string]bool{}
		}
		many[project][command] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	projectOf := map[int64]int64{}
	rows, err = tx.QueryContext(ctx, `SELECT r.id, d.project_id FROM document_revision r JOIN "document" d ON d.id = r.document_id
		WHERE d.kind = 'manifest' AND d.project_id IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, project int64
		if err := rows.Scan(&id, &project); err != nil {
			_ = rows.Close()
			return err
		}
		projectOf[id] = project
	}
	if err := rows.Close(); err != nil {
		return err
	}
	err = renameColumn(ctx, tx, "document_revision", "text", func(id int64, v string) string {
		project, ok := projectOf[id]
		if !ok {
			return v
		}
		return manifest16(v, many[project])
	})
	if err != nil {
		return err
	}
	byName := map[string]int64{}
	rows, err = tx.QueryContext(ctx, `SELECT id, workspace_id, name FROM project`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, ws int64
		var name string
		if err := rows.Scan(&id, &ws, &name); err != nil {
			_ = rows.Close()
			return err
		}
		byName[askKey(ws, name)] = id
	}
	if err := rows.Close(); err != nil {
		return err
	}
	workspaceOf := map[int64]int64{}
	rows, err = tx.QueryContext(ctx, `SELECT rowid, workspace_id FROM "ask"`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, ws int64
		if err := rows.Scan(&id, &ws); err != nil {
			_ = rows.Close()
			return err
		}
		workspaceOf[id] = ws
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return renameColumn(ctx, tx, "ask", "payload", func(id int64, v string) string {
		var p struct {
			Call struct {
				Input struct {
					Project  any    `json:"project"`
					Manifest string `json:"manifest"`
				} `json:"input"`
			} `json:"call"`
		}
		if json.Unmarshal([]byte(v), &p) != nil || p.Call.Input.Manifest == "" {
			return v
		}
		name, _ := p.Call.Input.Project.(string)
		raised := manifest16(p.Call.Input.Manifest, many[byName[askKey(workspaceOf[id], name)]])
		if raised == p.Call.Input.Manifest {
			return v
		}
		was, _ := json.Marshal(p.Call.Input.Manifest)
		now, _ := json.Marshal(raised)
		return strings.Replace(v, string(was), string(now), 1)
	})
}

func askKey(ws int64, name string) string { return fmt.Sprintf("%d/%s", ws, name) }

// manifest16 is a manifest of format 15 in format 16, its commands named in many given runs many; any other text as
// it is.
func manifest16(text string, many map[string]bool) string {
	top, ok := members([]byte(text))
	if !ok || !slices.ContainsFunc(top, func(m member) bool { return m.key == "version" && string(m.val) == "15" }) {
		return text
	}
	for i := range top {
		switch top[i].key {
		case "version":
			top[i].val = json.RawMessage("16")
		case "commands":
			var cmds []json.RawMessage
			if json.Unmarshal(top[i].val, &cmds) != nil {
				return text
			}
			for j, c := range cmds {
				cmds[j] = runsMany(c, many)
			}
			top[i].val = array(cmds)
		}
	}
	var buf bytes.Buffer
	if json.Indent(&buf, object(top), "", "  ") != nil {
		return text
	}
	return buf.String()
}

// runsMany is a command with runs many before its executor when many names it and it says nothing of runs.
func runsMany(c json.RawMessage, many map[string]bool) json.RawMessage {
	ms, ok := members(c)
	if !ok {
		return c
	}
	var name string
	at := len(ms)
	for i, m := range ms {
		switch m.key {
		case "runs":
			return c
		case "name":
			_ = json.Unmarshal(m.val, &name)
		case "executor":
			at = i
		}
	}
	if !many[name] {
		return c
	}
	ms = append(ms[:at], append([]member{{"runs", json.RawMessage(`"many"`)}}, ms[at:]...)...)
	return object(ms)
}

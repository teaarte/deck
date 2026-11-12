package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"

	"github.com/pressly/goose/v3"
)

// masterSleep puts the master's sleep in place of its memory in every revision of its manifest, the owner's too, so a
// merge with what deck ships keeps their other edits; the trigger keeps its pause, the assignment moves.
var masterSleep = goose.NewGoMigration(67, &goose.GoFunc{RunTx: toSleep}, nil)

// sleepCommand and sleepTrigger are sleep as deck first shipped it.
const (
	sleepCommand = `{"name": "sleep", "title": "Sleep", "description": "Once a night goes over what happened since the last sleep: writes, confirms and retires lessons with their source, proposes corrections of the owner's facts and of skills that keep failing the same way, and tidies the indexes.", "callers": ["owner", "trigger"], "provider": "free", "executor": {"type": "agent", "prompt_file": "prompts/sleep.md", "skills": ["memory"], "calls": ["deck.projects_list", "deck.documents_list", "deck.document_get", "deck.document_save", "deck.document_delete", "deck.memory_map", "deck.owner_notify"]}}`
	sleepTrigger = `{"command": "sleep", "cron": "30 3 * * *"}`
)

func toSleep(ctx context.Context, tx *sql.Tx) error {
	const master = `SELECT id FROM project WHERE builtin = 1`
	steps := []string{
		`UPDATE "trigger" SET command = 'sleep', spec = '30 3 * * *', next_run_at = NULL
			WHERE command = 'memory' AND kind = 'cron' AND project_id IN (` + master + `)`,
		`UPDATE assignment SET command = 'sleep' WHERE command = 'memory' AND project_id IN (` + master + `)`,
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	revs := map[int64]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT r.id FROM document_revision r JOIN "document" d ON d.id = r.document_id
		WHERE d.kind = 'manifest' AND d.project_id IN (`+master+`)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		revs[id] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return renameColumn(ctx, tx, "document_revision", "text", func(id int64, v string) string {
		if !revs[id] {
			return v
		}
		return withSleep(v)
	})
}

// withSleep is a manifest of the master with sleep in place of memory, in its commands and triggers; any other text
// as it is.
func withSleep(text string) string {
	top, ok := members([]byte(text))
	if !ok {
		return text
	}
	changed := false
	swap := func(key, field, with string) {
		for i := range top {
			if top[i].key != key {
				continue
			}
			var vs []json.RawMessage
			if json.Unmarshal(top[i].val, &vs) != nil {
				return
			}
			for j, v := range vs {
				var o map[string]any
				if json.Unmarshal(v, &o) == nil && o[field] == "memory" {
					vs[j], changed = json.RawMessage(with), true
				}
			}
			top[i].val = array(vs)
		}
	}
	swap("commands", "name", sleepCommand)
	swap("triggers", "command", sleepTrigger)
	if !changed {
		return text
	}
	var buf bytes.Buffer
	if json.Indent(&buf, object(top), "", "  ") != nil {
		return text
	}
	return buf.String()
}

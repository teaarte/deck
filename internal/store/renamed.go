package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/pressly/goose/v3"

	"deck2/schemas"
)

// renamed are deck's commands that format 14 of manifests named anew, old to new.
var renamed = map[string]string{
	"agent_logins": "agent_logins_list", "agent_login_code": "agent_login_finish",
	"browser_login_done": "browser_login_finish", "browser_needs_login": "browser_login_request",
	"chat": "chat_get", "chat_topics": "chat_topics_list", "chat_turn": "chat_turn_get", "chat_topic_model": "chat_topic_model_set",
	"connection_remove": "connection_delete", "folder_remove": "folder_delete", "model_remove": "model_delete",
	"files_folder": "files_folder_list", "layout": "layout_get", "manifest_format": "manifest_format_get",
	"theme_format": "theme_format_get", "menu": "menu_get", "notify": "owner_notify", "process_asks": "process_asks_list",
	"process_chat": "process_chat_get", "process_left": "process_left_list", "process_log": "process_log_get",
	"project_cover": "project_cover_set", "project_covers": "project_covers_list", "project_needs": "project_needs_get",
	"project_rights": "project_rights_list", "provider_off": "provider_switch", "push_devices": "push_devices_list",
	"push_key": "push_key_get", "push_reset": "push_key_reset", "record_parts": "record_parts_list", "screen": "screen_get",
	"screen_shot": "screen_capture", "secure_address": "secure_address_get", "spend_daily": "spend_days_list",
	"theme": "theme_get", "theme_image": "theme_image_set", "today": "today_get", "transcribe": "recording_transcribe",
	"update": "update_install", "version": "version_get", "health_seen": "health_mark_seen", "feed_read": "feed_mark_read",
}

// plain are the old names that are also plain words or fields of a manifest, renamed only as deck.<name>.
var plain = []string{"chat", "screen", "theme", "menu", "layout", "today", "version", "update", "transcribe"}

var qualified, quoted *regexp.Regexp

// deck is the prefix of the names of deck's commands.
const deck = schemas.Builtin + "."

func init() {
	var all, bare []string
	for old := range renamed {
		all = append(all, regexp.QuoteMeta(old))
		if !slices.Contains(plain, old) {
			bare = append(bare, regexp.QuoteMeta(old))
		}
	}
	long := func(a, b string) int { return len(b) - len(a) }
	slices.SortFunc(all, long)
	slices.SortFunc(bare, long)
	qualified = regexp.MustCompile(`\b` + regexp.QuoteMeta(deck) + `(` + strings.Join(all, "|") + `)\b`)
	quoted = regexp.MustCompile("([\"'`])(" + strings.Join(bare, "|") + ")([\"'`])")
}

// rename names deck's commands anew: deck.<old> everywhere, and in a text that is no manifest also a quoted old
// name, as a script or a prompt calls a tool.
func rename(text string, manifest bool) string {
	text = qualified.ReplaceAllStringFunc(text, func(m string) string { return deck + renamed[strings.TrimPrefix(m, deck)] })
	if manifest {
		return text
	}
	return quoted.ReplaceAllStringFunc(text, func(m string) string {
		if m[0] != m[len(m)-1] {
			return m
		}
		return m[:1] + renamed[m[1:len(m)-1]] + m[len(m)-1:]
	})
}

// format14 raises a manifest of format 13 to 14: its version, the one thing format 14 changed beside the names.
func format14(text string) string {
	dec := json.NewDecoder(strings.NewReader(text))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return text
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return text
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return text
		}
		if k == "version" {
			end := int(dec.InputOffset())
			if string(v) != "13" || !strings.HasSuffix(text[:end], "13") {
				return text
			}
			return text[:end-2] + "14" + text[end:]
		}
	}
	return text
}

// columns hold names of deck's commands: those a call goes by and the frozen calls, actors and buttons beside them.
var columns = map[string][]string{
	"trigger": {"command", "params"}, "process": {"command", "input", "allows"}, "tool_call": {"tool", "caller", "params"},
	"automation_token": {"commands"}, "feed": {"actions", "command", "params"}, "llm_call": {"command"},
	"ask": {"payload"}, "assignment": {"command"},
}

// commandsRenamed moves what deck keeps to the new names of its commands and stored manifests to format 14.
var commandsRenamed = goose.NewGoMigration(54, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
	for table, cols := range columns {
		for _, c := range cols {
			if err := renameColumn(ctx, tx, table, c, func(_ int64, v string) string { return rename(v, true) }); err != nil {
				return err
			}
		}
	}
	kinds := map[int64]string{}
	rows, err := tx.QueryContext(ctx, `SELECT r.id, d.kind FROM document_revision r JOIN "document" d ON d.id = r.document_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			_ = rows.Close()
			return err
		}
		kinds[id] = kind
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return renameColumn(ctx, tx, "document_revision", "text", func(id int64, v string) string {
		if kinds[id] == "manifest" {
			return format14(rename(v, true))
		}
		return rename(v, false)
	})
}}, nil)

// renameColumn writes f of each value of a column back where it changed.
func renameColumn(ctx context.Context, tx *sql.Tx, table, column string, f func(id int64, v string) string) error {
	rows, err := tx.QueryContext(ctx, `SELECT rowid, "`+column+`" FROM "`+table+`"`)
	if err != nil {
		return err
	}
	changed := map[int64]string{}
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			_ = rows.Close()
			return err
		}
		if w := f(id, v); w != v {
			changed[id] = w
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for id, v := range changed {
		if _, err := tx.ExecContext(ctx, `UPDATE "`+table+`" SET "`+column+`" = ? WHERE rowid = ?`, v, id); err != nil {
			return err
		}
	}
	return nil
}

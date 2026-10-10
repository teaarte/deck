package scenarios

import (
	"fmt"
)

// olderBase takes the base of deck back to the schema of a migration version, as a deck of then left it.
func (e *Env) olderBase(version int) {
	e.T.Helper()
	undo := map[int][]string{
		4: {`PRAGMA foreign_keys = OFF`, `CREATE TABLE ask_old (id INTEGER PRIMARY KEY AUTOINCREMENT,
		   workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		   project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
		   process_id INTEGER REFERENCES process(id) ON DELETE CASCADE, kind TEXT NOT NULL, text TEXT NOT NULL,
		   payload TEXT NOT NULL DEFAULT '{}', answer TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'open',
		   created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), answered_at TEXT, expires_at TEXT, delivered_at TEXT)`,
			`INSERT INTO ask_old SELECT * FROM ask`, `DROP TABLE ask`, `ALTER TABLE ask_old RENAME TO ask`, `PRAGMA foreign_keys = ON`},
		3: {`CREATE TABLE llm_old AS SELECT id, process_id, gen_ai_provider_name, gen_ai_request_model, gen_ai_response_model,
		   gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens,
		   gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd, created_at FROM llm_call`,
			`DROP TABLE llm_call`, `ALTER TABLE llm_old RENAME TO llm_call`},
		36: {},
		34: {`DROP TRIGGER project_gone`, `ALTER TABLE outbox DROP COLUMN path`},
		33: {`ALTER TABLE process DROP COLUMN reopened_at`},
		32: {`ALTER TABLE process DROP COLUMN dismissed`},
		31: {`ALTER TABLE llm_call DROP COLUMN subscription`, `ALTER TABLE model DROP COLUMN subscription`},
		30: {},
		29: {`ALTER TABLE llm_call DROP COLUMN session_cost_usd`},
		28: {`ALTER TABLE record DROP COLUMN process_id`, `ALTER TABLE feed DROP COLUMN process_id`},
		27: {},
		26: {`DROP TRIGGER record_added`, `DROP TRIGGER record_changed`, `DROP TRIGGER record_gone`, `DROP TABLE record_mark`},
		25: {`DROP INDEX document_lookup`},
		24: {`DROP TRIGGER file_gone`, `DROP TRIGGER process_closed`, `DROP TABLE outbox`, `ALTER TABLE ask DROP COLUMN unheard_at`},
		23: {`ALTER TABLE model DROP COLUMN unit`},
		22: {`ALTER TABLE health DROP COLUMN seen_at`},
		21: {},
		20: {`ALTER TABLE connection ADD COLUMN daily_usd REAL`},
		19: {`CREATE TABLE card (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
		   title TEXT NOT NULL, text TEXT NOT NULL DEFAULT '', lane TEXT NOT NULL, rank INTEGER NOT NULL,
		   record_id INTEGER REFERENCES record(id) ON DELETE SET NULL, process_id INTEGER REFERENCES process(id) ON DELETE SET NULL,
		   deleted_at TEXT, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		   updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`,
			`CREATE TABLE card_edge (card_id INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE,
		   waits_for INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE, PRIMARY KEY (card_id, waits_for))`,
			`ALTER TABLE document ADD COLUMN card_id INTEGER REFERENCES card(id) ON DELETE CASCADE`, `DROP INDEX document_name`,
			`CREATE UNIQUE INDEX document_name ON document (workspace_id, coalesce(project_id, 0), coalesce(record_id, 0), coalesce(card_id, 0), name)`,
			`ALTER TABLE chat_topic ADD COLUMN card_id INTEGER REFERENCES card(id) ON DELETE SET NULL`},
		18: {},
		17: {`DROP INDEX trigger_identity`, `CREATE UNIQUE INDEX trigger_identity ON "trigger" (project_id, command, kind, spec) WHERE kind != 'once'`},
		16: {},
		15: {},
		14: {`ALTER TABLE process ADD COLUMN pause_limit_ms INTEGER`},
		13: {},
		12: {`ALTER TABLE project ADD COLUMN model_daily_usd REAL`},
		11: {`DELETE FROM assignment WHERE role = 'stt' AND models IN ('["whisper"]', '["deck-whisper"]')`,
			`DELETE FROM model WHERE name IN ('whisper', 'deck-whisper') AND kind = 'stt'`,
			`DELETE FROM connection WHERE name IN ('whisper', 'deck-whisper') AND api = 'raw'`},
		10: {`DROP INDEX llm_call_connection`, `CREATE TABLE llm_old (id INTEGER PRIMARY KEY AUTOINCREMENT,
		   workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE, project_id INTEGER REFERENCES project(id) ON DELETE SET NULL,
		   process_id INTEGER REFERENCES process(id) ON DELETE SET NULL, command TEXT NOT NULL DEFAULT '', gen_ai_provider_name TEXT NOT NULL,
		   gen_ai_request_model TEXT NOT NULL DEFAULT '', gen_ai_response_model TEXT NOT NULL DEFAULT '',
		   gen_ai_usage_input_tokens INTEGER NOT NULL DEFAULT 0, gen_ai_usage_output_tokens INTEGER NOT NULL DEFAULT 0,
		   gen_ai_usage_cache_read_input_tokens INTEGER NOT NULL DEFAULT 0, gen_ai_usage_cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
		   duration_ms INTEGER NOT NULL DEFAULT 0, cost_usd REAL, created_at TEXT NOT NULL)`,
			`INSERT INTO llm_old SELECT id, workspace_id, project_id, process_id, command, gen_ai_provider_name, gen_ai_request_model,
		   gen_ai_response_model, gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens,
		   gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd, created_at FROM llm_call`,
			`DROP TABLE llm_call`, `ALTER TABLE llm_old RENAME TO llm_call`, `CREATE INDEX llm_call_process ON llm_call (process_id)`,
			`CREATE INDEX llm_call_workspace ON llm_call (workspace_id, created_at)`, `ALTER TABLE project DROP COLUMN model_daily_usd`},
		9: {`DROP TABLE assignment`, `DROP TABLE model`, `DROP TABLE connection`,
			`CREATE TABLE command_provider (project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
			   command TEXT NOT NULL, provider TEXT NOT NULL, PRIMARY KEY (project_id, command))`},
		8: {`ALTER TABLE process DROP COLUMN alone`},
		6: {`ALTER TABLE feed DROP COLUMN read_at`},
		5: {`ALTER TABLE project DROP COLUMN imported_from`, `ALTER TABLE feed DROP COLUMN for_master`},
		2: {`ALTER TABLE project DROP COLUMN granted_revision`},
	}
	var top int
	if err := e.App.Store.DB.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&top); err != nil {
		e.T.Fatal(err)
	}
	for v := top; v > version; v-- {
		for _, q := range append(undo[v], fmt.Sprintf(`DELETE FROM goose_db_version WHERE version_id = %d`, v)) {
			if _, err := e.App.Store.DB.Exec(q); err != nil {
				e.T.Fatalf("undo migration %d: %v", v, err)
			}
		}
	}
}

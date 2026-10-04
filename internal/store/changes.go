package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"slices"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Change is a topic of event a committed write brings: readers of the topic in the project re-read. Workspace
// zero and an empty project are every workspace and every project.
type Change struct {
	Topic     string
	Workspace int64
	Project   string
}

// table is what a write of a table changes: the topic its readers follow and the column of the row that leads to
// its project or workspace; quiet are columns whose change no reader shows.
type table struct {
	topic string
	// scope is project_id, workspace_id, or the id of the row of another table (process, card, document,
	// chat_topic) the project or workspace comes from.
	scope string
	quiet []string
}

var tables = map[string]table{
	"project":           {"projects", "id", nil},
	"project_folder":    {"projects", "project_id", nil},
	"command_provider":  {"projects", "project_id", nil},
	"trigger":           {"triggers", "project_id", nil},
	"process":           {"processes", "project_id", []string{"output_ms"}},
	"process_remark":    {"processes", "process_id", nil},
	"record":            {"collection", "project_id", nil},
	"card":              {"board", "project_id", nil},
	"card_edge":         {"board", "card_id", nil},
	"document":          {"documents", "project_id", nil},
	"document_revision": {"documents", "document_id", nil},
	"file":              {"files", "project_id", nil},
	"ask":               {"asks", "project_id", nil},
	"tool_call":         {"calls", "project_id", nil},
	"llm_call":          {"usage", "process_id", nil},
	"health":            {"health", "project_id", nil},
	"feed":              {"feed", "project_id", nil},
	"setting":           {"settings", "workspace_id", nil},
	"push_subscription": {"settings", "workspace_id", nil},
	"automation_token":  {"tokens", "workspace_id", nil},
	"chat_topic":        {"chat", "workspace_id", nil},
	"chat_message":      {"chat", "topic_id", nil},
}

// rowWrite is a row a transaction changed: the table and the scope ids of the old and the new row.
type rowWrite struct {
	table string
	ids   []int64
}

// changes keeps the writes of the open transaction until it commits, then until they are published.
type changes struct {
	mu        sync.Mutex
	columns   map[string][]string
	pending   []rowWrite
	committed []rowWrite
}

func (c *changes) preUpdate(d sqlite.SQLitePreUpdateData) {
	t, ok := tables[d.TableName]
	if !ok {
		return
	}
	c.mu.Lock()
	cols := c.columns[d.TableName]
	c.mu.Unlock()
	at := slices.Index(cols, t.scope)
	var old, new []any
	if d.Op != sqlite3.SQLITE_INSERT {
		old = make([]any, len(cols))
		if d.Old(old...) != nil {
			old = nil
		}
	}
	if d.Op != sqlite3.SQLITE_DELETE {
		new = make([]any, len(cols))
		if d.New(new...) != nil {
			new = nil
		}
	}
	if old != nil && new != nil && quiet(cols, t.quiet, old, new) {
		return
	}
	w := rowWrite{table: d.TableName}
	for _, row := range [][]any{old, new} {
		if id, ok := scopeID(row, at); ok && !slices.Contains(w.ids, id) {
			w.ids = append(w.ids, id)
		}
	}
	if len(w.ids) == 0 {
		w.ids = []int64{0}
	}
	c.mu.Lock()
	c.pending = append(c.pending, w)
	c.mu.Unlock()
}

// quiet reports whether an update changed only columns no reader shows.
func quiet(cols, skip []string, old, new []any) bool {
	if len(skip) == 0 {
		return false
	}
	for i, col := range cols {
		if !slices.Contains(skip, col) && !equal(old[i], new[i]) {
			return false
		}
	}
	return true
}

func equal(a, b any) bool {
	x, ok1 := a.([]byte)
	y, ok2 := b.([]byte)
	if ok1 || ok2 {
		return ok1 && ok2 && string(x) == string(y)
	}
	return a == b
}

func scopeID(row []any, at int) (int64, bool) {
	if row == nil || at < 0 || at >= len(row) {
		return 0, false
	}
	id, ok := row[at].(int64)
	return id, ok
}

func (c *changes) commit() int32 {
	c.mu.Lock()
	c.committed, c.pending = append(c.committed, c.pending...), nil
	c.mu.Unlock()
	return 0
}

func (c *changes) rollback() {
	c.mu.Lock()
	c.pending = nil
	c.mu.Unlock()
}

func (c *changes) take() []rowWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.committed
	c.committed = nil
	return out
}

// hooked opens connections that tell changes about every row they write.
type hooked struct {
	driver.Connector
	c *changes
}

func (h hooked) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := h.Connector.Connect(ctx)
	if r, ok := conn.(sqlite.HookRegisterer); ok && err == nil {
		r.RegisterPreUpdateHook(h.c.preUpdate)
		r.RegisterCommitHook(h.c.commit)
		r.RegisterRollbackHook(h.c.rollback)
	}
	return conn, err
}

// readColumns learns the columns of the tables whose writes are events, in their order.
func (c *changes) readColumns(conn *sql.DB) error {
	cols := map[string][]string{}
	for name := range tables {
		rows, err := conn.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, name)
		if err != nil {
			return err
		}
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				rows.Close()
				return err
			}
			cols[name] = append(cols[name], col)
		}
		rows.Close()
	}
	c.mu.Lock()
	c.columns, c.pending, c.committed = cols, nil, nil
	c.mu.Unlock()
	return nil
}

// resolve turns committed writes into changes: a row's project by name, or its workspace; a row whose project is
// gone changes every project of the topic.
func (s *Store) resolve(ctx context.Context, ws []rowWrite) []Change {
	var out []Change
	add := func(c Change) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, w := range ws {
		t := tables[w.table]
		for _, id := range w.ids {
			c := Change{Topic: t.topic}
			switch t.scope {
			case "workspace_id":
				c.Workspace = id
			case "topic_id":
				c.Workspace = s.one(ctx, `SELECT workspace_id FROM chat_topic WHERE id = ?`, id)
			default:
				project := id
				switch t.scope {
				case "process_id":
					project = s.one(ctx, `SELECT project_id FROM process WHERE id = ?`, id)
				case "card_id":
					project = s.one(ctx, `SELECT project_id FROM card WHERE id = ?`, id)
				case "document_id":
					project = s.one(ctx, `SELECT COALESCE(project_id, 0) FROM document WHERE id = ?`, id)
				}
				if project != 0 {
					var name string
					var ws int64
					if s.DB.QueryRowContext(ctx, `SELECT name, workspace_id FROM project WHERE id = ?`, project).Scan(&name, &ws) == nil {
						c.Project, c.Workspace = name, ws
					}
				}
			}
			add(c)
		}
	}
	return out
}

func (s *Store) one(ctx context.Context, q string, id int64) int64 {
	var v int64
	_ = s.DB.QueryRowContext(ctx, q, id).Scan(&v)
	return v
}

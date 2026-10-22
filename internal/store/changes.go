package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
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
	scope scope
	quiet []string
}

// scope is the column of a row its project or workspace comes from.
type scope int

const (
	// ofProject is the row's project_id, ofSelf the id of a project row.
	ofProject scope = iota
	ofSelf
	ofWorkspace
	// ofProcess, ofDocument and ofTopic are the id of a row of another table the project or workspace comes from.
	ofProcess
	ofDocument
	ofTopic
	// ofDeck is a row of deck itself, of no workspace.
	ofDeck
)

func (s scope) column() string {
	switch s {
	case ofProject:
		return "project_id"
	case ofSelf:
		return "id"
	case ofWorkspace:
		return "workspace_id"
	case ofProcess:
		return "process_id"
	case ofDocument:
		return "document_id"
	case ofTopic:
		return "topic_id"
	case ofDeck:
		return ""
	}
	panic(fmt.Sprintf("store: no column of scope %d", s))
}

var tables = map[string]table{
	"project":           {"projects", ofSelf, nil},
	"project_folder":    {"folders", ofProject, nil},
	"assignment":        {"assignments", ofProject, nil},
	"connection":        {"models", ofWorkspace, nil},
	"model":             {"models", ofWorkspace, nil},
	"mcp_tool":          {"tools", ofProject, nil},
	"trigger":           {"triggers", ofProject, nil},
	"process":           {"processes", ofProject, []string{"output_ms"}},
	"process_remark":    {"processes", ofProcess, nil},
	"record":            {"collection", ofProject, nil},
	"document":          {"documents", ofProject, nil},
	"document_revision": {"documents", ofDocument, nil},
	"file":              {"files", ofProject, nil},
	"ask":               {"asks", ofProject, nil},
	"tool_call":         {"calls", ofProject, nil},
	"llm_call":          {"usage", ofProject, nil},
	"health":            {"health", ofProject, nil},
	"feed":              {"feed", ofProject, nil},
	"setting":           {"settings", ofWorkspace, nil},
	"push_subscription": {"settings", ofWorkspace, nil},
	"automation_token":  {"tokens", ofWorkspace, nil},
	"chat_topic":        {"chat", ofWorkspace, nil},
	"chat_message":      {"chat", ofTopic, nil},
	"outbox":            {Outbox, ofDeck, []string{"tries", "error"}},
}

// Outbox is the topic of a row put in the outbox: deck's own, never published to a client.
const Outbox = "outbox"

// apart are the topics deck publishes by hand: of what it keeps in files or streams, not in tables.
var apart = []string{"log", "secrets", "snapshots"}

// Topics are every topic deck publishes: those of its tables and those it publishes apart.
func Topics() []string {
	out := slices.Clone(apart)
	for _, t := range tables {
		if t.topic != Outbox && !slices.Contains(out, t.topic) {
			out = append(out, t.topic)
		}
	}
	slices.Sort(out)
	return out
}

// silent are the tables no screen shows, so their writes publish nothing; every other table is in tables.
var silent = []string{"workspace", "goose_db_version", "mark", "folder_lock", "chat_session", "record_snapshot", "record_mark"}

// rowWrite is a row a transaction changed: the table and the scope ids of the old and the new row.
type rowWrite struct {
	table string
	ids   []int64
}

// changes keeps the writes of the open transaction, each once, until it commits, then until they are published.
type changes struct {
	mu        sync.Mutex
	columns   map[string][]string
	pending   []rowWrite
	seen      map[string]bool
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
	at := slices.Index(cols, t.scope.column())
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
	key := fmt.Sprint(w.table, w.ids)
	c.mu.Lock()
	if !c.seen[key] {
		if c.seen == nil {
			c.seen = map[string]bool{}
		}
		c.seen[key] = true
		c.pending = append(c.pending, w)
	}
	c.mu.Unlock()
}

// quiet reports whether an update changed only columns no reader shows, or nothing.
func quiet(cols, skip []string, old, new []any) bool {
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
	c.committed, c.pending, c.seen = append(c.committed, c.pending...), nil, nil
	c.mu.Unlock()
	return 0
}

func (c *changes) rollback() {
	c.mu.Lock()
	c.pending, c.seen = nil, nil
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

// readColumns learns the columns of the tables whose writes are events, in their order; a table of the base that
// is neither among them nor silent is an error.
func (c *changes) readColumns(conn *sql.DB) error {
	names, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return err
		}
		if _, ok := tables[name]; !ok && !slices.Contains(silent, name) {
			names.Close()
			return fmt.Errorf("store: table %s publishes no topic and is not silent", name)
		}
	}
	names.Close()
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
			project := int64(0)
			switch t.scope {
			case ofWorkspace:
				c.Workspace = id
			case ofTopic:
				c.Workspace = s.one(ctx, `SELECT workspace_id FROM chat_topic WHERE id = ?`, id)
			case ofDeck:
			case ofProject, ofSelf:
				project = id
			case ofProcess:
				project = s.one(ctx, `SELECT project_id FROM process WHERE id = ?`, id)
			case ofDocument:
				project = s.one(ctx, `SELECT COALESCE(project_id, 0) FROM document WHERE id = ?`, id)
			default:
				panic(fmt.Sprintf("store: no scope %d", t.scope))
			}
			if project != 0 {
				var name string
				var ws int64
				if s.reads.QueryRowContext(ctx, `SELECT name, workspace_id FROM project WHERE id = ?`, project).Scan(&name, &ws) == nil {
					c.Project, c.Workspace = name, ws
				}
			}
			add(c)
		}
	}
	return out
}

func (s *Store) one(ctx context.Context, q string, id int64) int64 {
	var v int64
	_ = s.reads.QueryRowContext(ctx, q, id).Scan(&v)
	return v
}

package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"deck2/internal/events"
	"fmt"
	"slices"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

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
	"project":           {events.Projects, ofSelf, nil},
	"project_folder":    {events.Folders, ofProject, nil},
	"assignment":        {events.Assignments, ofProject, nil},
	"connection":        {events.Models, ofWorkspace, nil},
	"model":             {events.Models, ofWorkspace, nil},
	"mcp_tool":          {events.Tools, ofProject, nil},
	"trigger":           {events.Triggers, ofProject, nil},
	"process":           {events.Processes, ofProject, []string{"output_ms"}},
	"process_remark":    {events.Processes, ofProcess, nil},
	"record":            {events.Collection, ofProject, nil},
	"document":          {events.Documents, ofProject, nil},
	"document_revision": {events.Documents, ofDocument, nil},
	"file":              {events.Files, ofProject, nil},
	"picture":           {events.Files, ofProject, nil},
	"ask":               {events.Asks, ofProject, nil},
	"tool_call":         {events.Calls, ofProject, nil},
	"llm_call":          {events.Usage, ofProject, nil},
	"health":            {events.Health, ofProject, nil},
	"feed":              {events.Feed, ofProject, nil},
	"setting":           {events.Settings, ofWorkspace, nil},
	"layout":            {events.Settings, ofWorkspace, nil},
	"automation_token":  {events.Devices, ofWorkspace, nil},
	"device":            {events.Devices, ofWorkspace, []string{"seen_at", "mark"}},
	"passkey":           {events.Devices, ofWorkspace, []string{"seen_at", "credential"}},
	"chat_topic":        {events.Chat, ofWorkspace, nil},
	"chat_message":      {events.Chat, ofTopic, nil},
	"outbox":            {events.Outbox, ofDeck, []string{"tries", "error"}},
}

func init() {
	for name, t := range tables {
		if t.topic != events.Outbox && !slices.Contains(events.Topics, t.topic) {
			panic(fmt.Sprintf("store: table %s publishes %s, not a topic of events", name, t.topic))
		}
	}
}

// silent are the tables no screen shows, so their writes publish nothing; every other table is in tables.
var silent = []string{"workspace", "goose_db_version", "mark", "folder_lock", "chat_session", "record_snapshot", "record_mark", "session",
	"memory_fts", "memory_fts_data", "memory_fts_idx", "memory_fts_content", "memory_fts_docsize", "memory_fts_config", "memory_read", "process_read",
	"ask_unheard"}

// rowWrite is a row a transaction changed: the table and where the old and the new row are.
type rowWrite struct {
	table  string
	scopes []scoped
}

// scoped is where a row is: the id its scope column gives and the workspace the row keeps, when it keeps one.
type scoped struct{ id, workspace int64 }

// place is where a row of a table others lead through is: its project, by name for a project row, and workspace.
type place struct {
	project, workspace int64
	name               string
}

// placed are the tables a row of another leads through to its project or workspace.
var placed = map[string]struct{ project, workspace string }{
	"project": {"id", "workspace_id"}, "process": {"project_id", ""}, "document": {"project_id", "workspace_id"},
	"chat_topic": {"", "workspace_id"},
}

// changes keeps the writes of the open transaction, each once, with where the rows others lead through were, until
// it commits, then until they are published.
type changes struct {
	mu        sync.Mutex
	columns   map[string][]string
	pending   []rowWrite
	seen      map[string]bool
	places    map[string]map[int64]place
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
	at, wsAt := slices.Index(cols, t.scope.column()), slices.Index(cols, "workspace_id")
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
		if row == nil {
			continue
		}
		id, _ := scopeID(row, at)
		ws, _ := scopeID(row, wsAt)
		if sc := (scoped{id, ws}); !slices.Contains(w.scopes, sc) {
			w.scopes = append(w.scopes, sc)
		}
	}
	if len(w.scopes) == 0 {
		w.scopes = []scoped{{}}
	}
	key := fmt.Sprint(w.table, w.scopes)
	c.mu.Lock()
	defer c.mu.Unlock()
	if pl, ok := placed[d.TableName]; ok {
		c.place(d.TableName, cols, pl.project, pl.workspace, old, new)
	}
	if !c.seen[key] {
		if c.seen == nil {
			c.seen = map[string]bool{}
		}
		c.seen[key] = true
		c.pending = append(c.pending, w)
	}
}

// place keeps where a row others lead through was when the transaction first wrote it.
func (c *changes) place(table string, cols []string, project, workspace string, rows ...[]any) {
	for _, row := range rows {
		id, ok := scopeID(row, slices.Index(cols, "id"))
		if !ok || c.places[table][id] != (place{}) {
			continue
		}
		p := place{}
		p.project, _ = scopeID(row, slices.Index(cols, project))
		p.workspace, _ = scopeID(row, slices.Index(cols, workspace))
		if i := slices.Index(cols, "name"); table == "project" && i >= 0 {
			p.name, _ = row[i].(string)
		}
		if c.places == nil {
			c.places = map[string]map[int64]place{}
		}
		if c.places[table] == nil {
			c.places[table] = map[int64]place{}
		}
		c.places[table][id] = p
	}
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
	c.committed, c.pending, c.seen, c.places = append(c.committed, c.pending...), nil, nil, nil
	c.mu.Unlock()
	return 0
}

func (c *changes) rollback() {
	c.mu.Lock()
	c.pending, c.seen, c.places = nil, nil, nil
	c.mu.Unlock()
}

// open takes the writes of the transaction about to commit, with where the rows others lead through were.
func (c *changes) open() ([]rowWrite, map[string]map[int64]place) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ws, places := c.pending, c.places
	c.pending, c.seen, c.places = nil, nil, nil
	return ws, places
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

// query reads one row, inside the transaction before its commit or after it.
type query func(ctx context.Context, q string, args ...any) *sql.Row

// lookups read where a row of a table others lead through is.
var lookups = map[string]string{
	"project":    `SELECT id, workspace_id, name FROM project WHERE id = ?`,
	"process":    `SELECT project_id, 0, '' FROM process WHERE id = ?`,
	"document":   `SELECT COALESCE(project_id, 0), workspace_id, '' FROM document WHERE id = ?`,
	"chat_topic": `SELECT 0, workspace_id, '' FROM chat_topic WHERE id = ?`,
}

// at is where a row of a table others lead through is, from places when the transaction knew it; zero when deck
// cannot find it.
func at(ctx context.Context, one query, places map[string]map[int64]place, table string, id int64) place {
	if p, ok := places[table][id]; ok {
		return p
	}
	var p place
	_ = one(ctx, lookups[table], id).Scan(&p.project, &p.workspace, &p.name)
	return p
}

// inProject puts the project and workspace of a project row into an event when deck finds the row.
func inProject(ctx context.Context, one query, places map[string]map[int64]place, c *events.Event, project int64) {
	if project == 0 {
		return
	}
	if p := at(ctx, one, places, "project", project); p.name != "" {
		c.Project, c.Workspace = p.name, p.workspace
	}
}

// ProcessEvent is an event of a topic in the project and workspace of a process, read outside a transaction; deck
// publishes it without them when it cannot find the process.
func (s *Store) ProcessEvent(ctx context.Context, processID int64, topic string) events.Event {
	check(ctx)
	e := events.Event{Topic: topic}
	inProject(ctx, s.reads.QueryRowContext, nil, &e, at(ctx, s.reads.QueryRowContext, nil, "process", processID).project)
	return e
}

// resolve turns writes into changes: a row's project by name with its workspace, or the workspace the row keeps; a row
// whose project deck cannot find changes every project of the topic in its workspace.
func resolve(ctx context.Context, one query, ws []rowWrite, places map[string]map[int64]place) []events.Event {
	var out []events.Event
	add := func(c events.Event) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, w := range ws {
		t := tables[w.table]
		for _, sc := range w.scopes {
			c := events.Event{Topic: t.topic, Workspace: sc.workspace}
			project := int64(0)
			switch t.scope {
			case ofDeck:
			case ofWorkspace:
				c.Workspace = sc.id
			case ofTopic:
				c.Workspace = at(ctx, one, places, "chat_topic", sc.id).workspace
			case ofProject, ofSelf:
				project = sc.id
			case ofProcess:
				project = at(ctx, one, places, "process", sc.id).project
			case ofDocument:
				d := at(ctx, one, places, "document", sc.id)
				project, c.Workspace = d.project, d.workspace
			default:
				panic(fmt.Sprintf("store: no scope %d", t.scope))
			}
			inProject(ctx, one, places, &c, project)
			add(c)
		}
	}
	return out
}

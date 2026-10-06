package store

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"

	"deck2/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store holds one SQLite connection for writes, so transactions run one at a time and never interleave, and
// connections that only read beside it; Q reads through those.
type Store struct {
	DB    *sql.DB
	Q     *db.Queries
	reads *sql.DB
	uri   string
	// Changed hears what each committed transaction changed, once it committed.
	Changed func(Change)
	// Wait is how long a transaction waits for the base before it fails.
	Wait    time.Duration
	changes *changes
}

// TimeLayout is how deck stores times as text; the text sorts like the times.
const TimeLayout = "2006-01-02T15:04:05.000Z"

func Time(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ID is a row id for a column that may point nowhere: 0 is NULL.
func ID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

// Project statuses: a draft runs only trial runs and no triggers until the owner enables it.
const (
	ProjectDraft   = "draft"
	ProjectEnabled = "enabled"
)

// DefaultWorkspace is the workspace the owner's token opens; migrations create it.
const DefaultWorkspace = 1

type txKey struct{}

// guard fails loudly when code queries through the shared connection inside a transaction, which would wait
// forever for the connection the transaction holds, or writes through it outside one.
type guard struct{ db *sql.DB }

func check(ctx context.Context) {
	if ctx.Value(txKey{}) != nil {
		panic("store: query through Store.Q inside a transaction; use the transaction's queries")
	}
}

func checkRead(ctx context.Context, q string) {
	check(ctx)
	if write(q) {
		panic("store: write through Store.Q outside a transaction; use Store.Tx: " + strings.SplitN(q, "\n", 2)[0])
	}
}

// write reports whether a statement changes the base, past the comment lines sqlc puts first.
func write(q string) bool {
	for _, line := range strings.Split(q, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		verb, _, _ := strings.Cut(strings.ToUpper(line), " ")
		return verb == "INSERT" || verb == "UPDATE" || verb == "DELETE" || verb == "REPLACE"
	}
	return false
}

func (g guard) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	checkRead(ctx, q)
	return g.db.ExecContext(ctx, q, a...)
}

func (g guard) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	checkRead(ctx, q)
	return g.db.PrepareContext(ctx, q)
}

func (g guard) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	checkRead(ctx, q)
	return g.db.QueryContext(ctx, q, a...)
}

func (g guard) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	checkRead(ctx, q)
	return g.db.QueryRowContext(ctx, q, a...)
}

func Open(path string) (*Store, error) {
	uri := "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	base, err := sqlite.NewConnector(uri + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	ch := &changes{}
	conn := sql.OpenDB(hooked{base, ch})
	conn.SetMaxOpenConns(1)
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.Up(conn, "migrations"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := ch.readColumns(conn); err != nil {
		return nil, err
	}
	reads, err := sql.Open("sqlite", uri+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	reads.SetMaxOpenConns(4)
	return &Store{DB: conn, Q: db.New(guard{reads}), reads: reads, uri: uri, changes: ch}, nil
}

func (s *Store) Close() error { return cmp.Or(s.reads.Close(), s.DB.Close()) }

// Backup writes a copy of the base to path from a connection of its own, so writes go on meanwhile.
func (s *Store) Backup(ctx context.Context, path string) error {
	conn, err := sql.Open("sqlite", s.uri+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

// writer takes the connection that writes, waiting at most Wait: a transaction that waits longer is held by
// another, often one that called this through a context not of its own, and fails with its stack in the log.
func (s *Store) writer(ctx context.Context) (*sql.Conn, error) {
	wait := cmp.Or(s.Wait, 30*time.Second)
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	conn, err := s.DB.Conn(wctx)
	if err != nil && ctx.Err() == nil && wctx.Err() != nil {
		slog.Error("store: a transaction waited too long for the base", "wait", wait, "stack", string(debug.Stack()))
		return nil, fmt.Errorf("store: a transaction waited %s for the base: another holds it, perhaps the one that called this with a context not of its own", wait)
	}
	return conn, err
}

// TxGet runs f in a transaction and returns what it gives.
func TxGet[T any](s *Store, ctx context.Context, f func(ctx context.Context, q *db.Queries) (T, error)) (T, error) {
	var out T
	err := s.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		out, err = f(ctx, q)
		return err
	})
	return out, err
}

// Tx runs f in one transaction and commits when it returns nil. Inside f, use only q and the ctx it gets.
func (s *Store) Tx(ctx context.Context, f func(ctx context.Context, q *db.Queries) error) error {
	check(ctx)
	conn, err := s.writer(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := f(context.WithValue(ctx, txKey{}, true), s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if ws := s.changes.take(); len(ws) > 0 && s.Changed != nil {
		for _, c := range s.resolve(ctx, ws) {
			s.Changed(c)
		}
	}
	return nil
}

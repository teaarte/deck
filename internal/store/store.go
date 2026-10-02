package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"deck2/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store holds one SQLite connection, so transactions run one at a time and never interleave.
type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

// TimeLayout is how deck stores times as text; the text sorts like the times.
const TimeLayout = "2006-01-02T15:04:05.000Z"

func Time(t time.Time) string { return t.UTC().Format(TimeLayout) }

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
	uri := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	conn, err := sql.Open("sqlite", "file:"+uri+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.Up(conn, "migrations"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{DB: conn, Q: db.New(guard{conn})}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

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
	tx, err := s.DB.BeginTx(ctx, nil)
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
	return tx.Commit()
}

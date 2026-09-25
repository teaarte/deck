package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

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

// DefaultWorkspace is the workspace the owner's token opens; migrations create it.
const DefaultWorkspace = 1

type txKey struct{}

// guard fails loudly when code inside a transaction queries through the shared connection, which would
// wait forever for the connection the transaction holds.
type guard struct{ db *sql.DB }

func check(ctx context.Context) {
	if ctx.Value(txKey{}) != nil {
		panic("store: query through Store.Q inside a transaction; use the transaction's queries")
	}
}

func (g guard) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	check(ctx)
	return g.db.ExecContext(ctx, q, a...)
}

func (g guard) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	check(ctx)
	return g.db.PrepareContext(ctx, q)
}

func (g guard) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	check(ctx)
	return g.db.QueryContext(ctx, q, a...)
}

func (g guard) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	check(ctx)
	return g.db.QueryRowContext(ctx, q, a...)
}

func Open(path string) (*Store, error) {
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path))
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

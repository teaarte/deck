// Package upgrade raises what an older deck left to what this one reads: the Go migrations of the base, run by
// store.Open beside its SQL ones, and the manifests a module brings from outside.
package upgrade

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"deck2/schemas"
)

// Migrations are the Go migrations of the base, by version.
var Migrations = append([]*goose.Migration{}, masterSleep, format17)

// Floor is the last migration of v0.2.0, the oldest release a base updates from.
const Floor = 64

// Floored refuses a base of a deck older than v0.2.0: it updates to v0.3.2 first.
func Floored(from, _ int64, _ func(string) error) error {
	if from < Floor {
		return fmt.Errorf("the base was left by a deck before v0.2.0 (migration %d): update it to v0.3.2 first, then to this "+
			"release (docs/install.md, \"Updating from an older release\")", from)
	}
	return nil
}

// deck is the prefix of the names of deck's commands.
const deck = schemas.Builtin + "."

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

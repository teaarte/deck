package store

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"

	"deck2/internal/manifest"
)

// raise drops the marks of read health deck no longer keeps and raises the stored manifests.
var raise = goose.NewGoMigration(43, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM mark WHERE name GLOB 'health-seen:*'`); err != nil {
		return err
	}
	return raiseManifests(ctx, tx)
}}, nil)

// raiseManifests stores every revision of a manifest in the current format and the one stored form; a revision
// that does not raise stays as it was. A new format of manifests raises them with a migration of its own.
func raiseManifests(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.text FROM document_revision r JOIN document d ON d.id = r.document_id
		WHERE d.kind = 'manifest'`)
	if err != nil {
		return err
	}
	raised := map[int64]string{}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return err
		}
		if up, _, err := manifest.Upgrade([]byte(text)); err == nil && manifest.Canonical(string(up)) != text {
			raised[id] = manifest.Canonical(string(up))
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for id, text := range raised {
		if _, err := tx.ExecContext(ctx, `UPDATE document_revision SET text = ? WHERE id = ?`, text, id); err != nil {
			return err
		}
	}
	return nil
}

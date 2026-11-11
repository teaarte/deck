package owner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// olderBase takes the base of e back to an older deck's, which the next start migrates.
func olderBase(e *harness.Env) {
	e.T.Helper()
	for _, q := range harness.BeforePasskeys {
		// The base of a deck before passkeys, to be migrated.
		e.SQL(q)
	}
}

// TestAnUpdateKeepsTheBaseItMigrates: a start that migrates the base first keeps it in a snapshot of its own, and the
// steps of docs/install.md, "Restoring a snapshot", put that base back for the deck it was of.
func TestAnUpdateKeepsTheBaseItMigrates(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	h := deckhome.Layout(e.App.Cfg.Home)
	e.Project("kept", harness.PlainManifest)
	olderBase(e)
	var older int64
	// The migration of the base, which deck shows nowhere.
	if err := e.SQLRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&older); err != nil {
		t.Fatal(err)
	}
	e = e.Restart()
	var list []struct{ Name, Path string }
	e.Must("deck.snapshots_list", nil, &list)
	if len(list) != 1 || !strings.HasPrefix(list[0].Name, "update-") {
		t.Fatalf("the start that migrated the base kept it first, as update-<time>: %+v", list)
	}
	e.Project("after", harness.PlainManifest)
	e = e.Restart()
	if e.Must("deck.snapshots_list", nil, &list); len(list) != 1 {
		t.Fatalf("a start with nothing to migrate keeps no snapshot: %+v", list)
	}
	// As after a migration failed halfway, the same deck starts on a base to migrate again.
	olderBase(e)
	e = e.Restart()
	if e.Must("deck.snapshots_list", nil, &list); len(list) != 1 {
		t.Fatalf("the same deck starting again keeps the snapshot of the base before its first start: %+v", list)
	}
	e.Stop()

	// docs/install.md, "Restoring a snapshot": deck stopped, its base and journal files go, the base of the snapshot
	// comes in their place.
	for _, f := range []string{"deck.db", "deck.db-wal", "deck.db-shm"} {
		if err := os.Remove(filepath.Join(filepath.Dir(h.DB()), f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("tar", "-xzf", list[0].Path, "-C", string(h), "state/deck.db").CombinedOutput(); err != nil {
		t.Fatalf("the snapshot holds state/deck.db: %v %s", err, out)
	}
	back := harness.OpenBase(t, h.DB())
	var version int64
	if err := back.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil || version != older {
		t.Fatalf("the snapshot holds the base before the update, at %d: %d %v", older, version, err)
	}
	var names []string
	rows, err := back.Query(`SELECT name FROM project WHERE name IN ('kept', 'after') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	back.Close()
	if strings.Join(names, ",") != "kept" {
		t.Fatalf("the base is as it was before the update: %v", names)
	}
}

// TestAnOlderDeckStartsNotOnABaseANewerOneMigrated: a deck whose base a newer deck migrated refuses to start and says
// what to do, rather than run on a schema and manifests it does not know.
func TestAnOlderDeckStartsNotOnABaseANewerOneMigrated(t *testing.T) {
	t.Parallel()
	bin := buildDeck(t)
	e := harness.New(t)
	// A migration of a newer deck.
	e.SQL(`INSERT INTO goose_db_version (version_id, is_applied) VALUES ((SELECT max(version_id) + 1 FROM goose_db_version), 1)`)
	e.Stop()
	// A deck that starts serves until the minute is out.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "DECK_HOME="+e.Home)
	out, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil {
		t.Fatalf("deck refuses to start on a base a newer deck migrated: %v\n%s", err, out)
	}
	for _, want := range []string{"a newer deck", "update-", "Restoring a snapshot"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("the refusal says what happened and what to do (%q):\n%s", want, out)
		}
	}
	t.Logf("%s", out)
}

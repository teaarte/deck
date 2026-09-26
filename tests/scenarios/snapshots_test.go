package scenarios

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailySnapshotKeepsTheNewestSeven(t *testing.T) {
	e := newClockEnv(t, nil)
	backups := func() int {
		m, _ := filepath.Glob(filepath.Join(e.App.Cfg.Home, "backups", "deck-*.tar.gz"))
		return len(m)
	}
	e.sweep()
	if n := backups(); n != 1 {
		t.Fatalf("the first sweep takes a snapshot: %d", n)
	}
	e.Clock.Add(23 * time.Hour)
	e.sweep()
	if n := backups(); n != 1 {
		t.Fatalf("one snapshot a day: %d", n)
	}
	for range 9 {
		e.Clock.Add(24 * time.Hour)
		e.sweep()
	}
	if n := backups(); n != 7 {
		t.Fatalf("the newest seven stay: %d", n)
	}
	if h := e.waitHealth("disk", ""); h.Error == "" {
		t.Fatalf("the disk health shows files and free space: %+v", h)
	}
}

func TestFilesOfDeletedRecordsGoInTheDailyCleanup(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("files", scriptManifest("make", "", "", ""))
	e.saveScript("files", "scripts/main.py", pythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
open(os.path.join(os.environ["DECK_RESULTS"], "f.txt"), "w").write("f")
print(json.dumps({"files": [{"path": "f.txt", "record": out["ids"][0]}]}))
`)
	if p := e.waitScript(e.start("files", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script binds a file: %+v", p)
	}
	path := filepath.Join(e.App.Cfg.Home, "files", "1")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	e.must("deck.collection_delete", map[string]any{"project": "files", "collection": "items"}, nil)
	e.sweep()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file of a deleted record is removed: %v", err)
	}
}

package scenarios

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailySnapshotKeepsTheNewestSeven(t *testing.T) {
	e := newClockEnv(t, nil)
	backups := func() int {
		m, _ := filepath.Glob(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "backups", "deck-*.tar.gz"))
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
	e.createProject("files", scriptManifest("make", "", ""))
	e.saveScript("files", "scripts/main.py", pythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
open(os.path.join(os.environ["DECK_RESULTS"], "f.txt"), "w").write("f")
print(json.dumps({"files": [{"path": "f.txt", "record": out["ids"][0]}]}))
`)
	if p := e.waitScript(e.start("files", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script binds a file: %+v", p)
	}
	path := filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", "1")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	e.must("deck.collection_delete", map[string]any{"project": "files", "collection": "items"}, nil)
	e.sweep()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file of a deleted record is removed: %v", err)
	}
}

func TestAHandSnapshotDoesNotPutOffTheDailyCleanup(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("docs", filesManifest())
	e.sweep()
	e.upload("docs", "left.txt", "text/plain", "left")
	e.Clock.Add(24*time.Hour + 30*time.Minute)
	e.must("deck.snapshot_create", nil, nil)
	e.Clock.Add(time.Hour)
	e.sweep()
	var uploads int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE owner_kind = 'upload'`).Scan(&uploads)
	if uploads != 0 {
		t.Fatalf("the cleanup runs a day after the last one, whatever snapshot came between: %d uploads left", uploads)
	}
}

const manyFilesScript = pythonLib + `
files = []
for i in range(40):
    st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "r%d" % i})})
    with open(os.path.join(os.environ["DECK_RESULTS"], "f%d.bin" % i), "wb") as f:
        f.write(os.urandom(256 << 10))
    files.append({"path": "f%d.bin" % i, "record": out["ids"][0]})
print(json.dumps({"files": files}))
`

func TestASnapshotHoldsEveryFileItsBaseNames(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("many", scriptManifest("make", "", ""))
	e.saveScript("many", "scripts/main.py", manyFilesScript)
	if p := e.waitScript(e.start("many", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("seed: %+v", p)
	}
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "many", "collection": "items", "limit": 100}, &recs)
	for round := range 5 {
		var path struct{ Path string }
		done := make(chan string, 1)
		go func() {
			code, out := e.call("deck.snapshot_create", nil)
			_ = json.Unmarshal(out, &path)
			done <- fmt.Sprint(code, " ", string(out))
		}()
		for _, r := range recs[round*8 : round*8+8] {
			e.must("deck.record_delete", map[string]any{"record": r.ID}, nil)
		}
		if got := <-done; path.Path == "" {
			t.Fatalf("round %d: the snapshot is taken while records go: %s", round, got)
		}
		named, stored := snapshotFiles(t, path.Path)
		for _, id := range named {
			if !stored["state/files/"+id] {
				t.Fatalf("round %d: the base of the snapshot names file %s, the archive lacks it", round, id)
			}
		}
	}
}

// snapshotFiles reads a snapshot: the file ids its base names and the entries of the archive.
func snapshotFiles(t *testing.T, path string) ([]string, map[string]bool) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	stored := map[string]bool{}
	dbPath := filepath.Join(t.TempDir(), "deck.db")
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		stored[h.Name] = true
		if h.Name == "state/deck.db" {
			out, _ := os.Create(dbPath)
			_, _ = io.Copy(out, tr)
			_ = out.Close()
		}
	}
	conn, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(`SELECT id FROM file WHERE owner_kind = 'record'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var named []string
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		named = append(named, fmt.Sprint(id))
	}
	return named, stored
}

// TestSnapshotsBeforeDeletesKeepTheirOwnTurn: snapshots taken before deleting projects rotate among themselves
// and never push out the daily ones.
func TestSnapshotsBeforeDeletesKeepTheirOwnTurn(t *testing.T) {
	e := newClockEnv(t, nil)
	backups := func(pattern string) int {
		m, _ := filepath.Glob(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "backups", pattern))
		return len(m)
	}
	e.sweep()
	for i := range 9 {
		e.Clock.Add(time.Minute)
		name := fmt.Sprintf("p%d", i)
		e.createProject(name, plainManifest)
		e.must("deck.project_delete", map[string]any{"project": name}, nil)
	}
	if n := backups("deck-*.tar.gz"); n != 1 {
		t.Fatalf("the daily snapshot stays after nine deletes: %d", n)
	}
	if n := backups("*.tar.gz"); n != 8 {
		t.Fatalf("the newest seven snapshots before a delete stay beside it: %d", n)
	}
	var list []struct {
		Name    string
		TakenAt string `json:"taken_at"`
	}
	e.must("deck.snapshots_list", nil, &list)
	if len(list) != 8 || list[0].TakenAt == "" {
		t.Fatalf("the journal lists both kinds with their time: %+v", list)
	}
}

// TestADailySnapshotSkipsAFileItCannotReadAndSaysSo: a file of a data folder the snapshot cannot read is left out
// with a warning in health, the rest is kept; a snapshot that fails is an error in health, not only in the log.
func TestADailySnapshotSkipsAFileItCannotReadAndSaysSo(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("notes", ideasManifest)
	h := deckhome.Layout(e.App.Cfg.Home)
	data, _ := filepath.Glob(filepath.Join(h.Projects(), "*", "data"))
	if len(data) == 0 {
		data = []string{filepath.Join(h.Projects(), "1", "data")}
	}
	if err := os.MkdirAll(data[0], 0o700); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(data[0], "locked.bin")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data[0], "kept.txt"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	snaps, _ := filepath.Glob(filepath.Join(h.Backups(), "deck-*.tar.gz"))
	if len(snaps) != 1 {
		t.Fatalf("one unreadable file does not stop the daily snapshot: %v", snaps)
	}
	_, stored := snapshotFiles(t, snaps[0])
	kept := false
	for name := range stored {
		kept = kept || filepath.Base(name) == "kept.txt"
	}
	if !kept {
		t.Fatalf("the rest of the data folder is in the snapshot: %v", stored)
	}
	if row := e.waitHealth("snapshot", "warning"); !strings.Contains(row.Error, "locked.bin") {
		t.Fatalf("the health of snapshots names the file left out: %+v", row)
	}
	_ = os.Chmod(locked, 0o600)

	if err := os.RemoveAll(h.Backups()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.Backups(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	if row := e.waitHealth("snapshot", "error"); row.Error == "" {
		t.Fatalf("a snapshot that fails says why in health: %+v", row)
	}
	_ = os.Remove(h.Backups())
	left := filepath.Join(h.Backups(), ".files-crashed")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(left, "7"), []byte("a file the owner deleted"), 0o600)
	e.Clock.Add(time.Hour)
	e.sweep()
	e.waitHealth("snapshot", "ok")
	if _, err := os.Stat(left); err == nil {
		t.Fatal("the links a snapshot cut short by a crash left go with the next snapshot")
	}
}

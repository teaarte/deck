package scenarios

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDailySnapshotKeepsTheNewestSeven(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	backups := func() int {
		m, _ := filepath.Glob(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "backups", "deck-*.tar.gz"))
		return len(m)
	}
	e.Sweep()
	if n := backups(); n != 1 {
		t.Fatalf("the first sweep takes a snapshot: %d", n)
	}
	e.Clock.Add(23 * time.Hour)
	e.Sweep()
	if n := backups(); n != 1 {
		t.Fatalf("one snapshot a day: %d", n)
	}
	for range 9 {
		e.Clock.Add(24 * time.Hour)
		e.Sweep()
	}
	if n := backups(); n != 7 {
		t.Fatalf("the newest seven stay: %d", n)
	}
	if h := e.WaitHealth("disk", ""); h.Error == "" {
		t.Fatalf("the disk health shows files and free space: %+v", h)
	}
}

func TestFilesOfDeletedRecordsGoInTheDailyCleanup(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("files", harness.ScriptManifest("make", "", ""))
	e.SaveScript("files", "scripts/main.py", harness.PythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
open(os.path.join(os.environ["DECK_RESULTS"], "f.txt"), "w").write("f")
print(json.dumps({"files": [{"path": "f.txt", "record": out["ids"][0]}]}))
`)
	if p := e.WaitState(e.Start("files", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script binds a file: %+v", p)
	}
	path := filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", "1")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.collection_delete", map[string]any{"project": "files", "collection": "items"}, nil)
	e.Sweep()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file of a deleted record is removed: %v", err)
	}
}

func TestAHandSnapshotDoesNotPutOffTheDailyCleanup(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("docs", filesManifest())
	e.Sweep()
	e.Upload("docs", "left.txt", "text/plain", "left")
	e.Clock.Add(24*time.Hour + 30*time.Minute)
	e.Must("deck.snapshot_create", nil, nil)
	e.Clock.Add(time.Hour)
	e.Sweep()
	var uploads int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE owner_kind = 'upload'`).Scan(&uploads)
	if uploads != 0 {
		t.Fatalf("the cleanup runs a day after the last one, whatever snapshot came between: %d uploads left", uploads)
	}
}

const manyFilesScript = harness.PythonLib + `
files = []
for i in range(40):
    st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "r%d" % i})})
    with open(os.path.join(os.environ["DECK_RESULTS"], "f%d.bin" % i), "wb") as f:
        f.write(os.urandom(256 << 10))
    files.append({"path": "f%d.bin" % i, "record": out["ids"][0]})
print(json.dumps({"files": files}))
`

func TestASnapshotHoldsEveryFileItsBaseNames(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("many", harness.ScriptManifest("make", "", ""))
	e.SaveScript("many", "scripts/main.py", manyFilesScript)
	if p := e.WaitState(e.Start("many", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("seed: %+v", p)
	}
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "many", "collection": "items", "limit": 100}, &recs)
	for round := range 5 {
		var path struct{ Path string }
		done := make(chan string, 1)
		go func() {
			code, out := e.Call("deck.snapshot_create", nil)
			_ = json.Unmarshal(out, &path)
			done <- fmt.Sprint(code, " ", string(out))
		}()
		for _, r := range recs[round*8 : round*8+8] {
			e.Must("deck.record_delete", map[string]any{"record": r.ID}, nil)
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
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	backups := func(pattern string) int {
		m, _ := filepath.Glob(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "backups", pattern))
		return len(m)
	}
	e.Sweep()
	for i := range 9 {
		e.Clock.Add(time.Minute)
		name := fmt.Sprintf("p%d", i)
		e.Project(name, harness.PlainManifest)
		e.Must("deck.project_delete", map[string]any{"project": name}, nil)
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
	e.Must("deck.snapshots_list", nil, &list)
	if len(list) != 8 || list[0].TakenAt == "" {
		t.Fatalf("the journal lists both kinds with their time: %+v", list)
	}
}

// TestASnapshotBeforeADeleteHoldsOnlyThatProject: the snapshot before a delete keeps the base, the stored files and
// the data folder of the deleted project, and nothing of the other projects.
func TestASnapshotBeforeADeleteHoldsOnlyThatProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	h := deckhome.Layout(e.App.Cfg.Home)
	stored := map[string]string{}
	for _, name := range []string{"keep", "gone"} {
		e.Project(name, harness.PlainManifest)
		var id int64
		if err := e.App.Store.DB.QueryRow(`SELECT id FROM project WHERE name = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(h.Data(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Data(id), name+".txt"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		var file int64
		if err := e.App.Store.DB.QueryRow(`INSERT INTO file (project_id, owner_kind, owner_id, name) VALUES (?, 'project', ?, ?) RETURNING id`,
			id, id, name+".bin").Scan(&file); err != nil {
			t.Fatal(err)
		}
		_ = os.MkdirAll(h.Files(), 0o700)
		if err := os.WriteFile(filepath.Join(h.Files(), fmt.Sprint(file)), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		stored[name] = fmt.Sprint(file)
	}
	var out struct{ Snapshot string }
	e.Must("deck.project_delete", map[string]any{"project": "gone"}, &out)
	_, entries := snapshotFiles(t, out.Snapshot)
	var data []string
	for name := range entries {
		if strings.HasSuffix(name, ".txt") {
			data = append(data, filepath.Base(name))
		}
	}
	if !entries["state/deck.db"] || !entries["state/files/"+stored["gone"]] || entries["state/files/"+stored["keep"]] ||
		len(data) != 1 || data[0] != "gone.txt" {
		t.Fatalf("the snapshot holds the base and the files of the deleted project only: %v", entries)
	}
}

// TestADailySnapshotSkipsAFileItCannotReadAndSaysSo: a file of a data folder the snapshot cannot read is left out
// with a warning in health, the rest is kept; a snapshot that fails is an error in health, not only in the log.
func TestADailySnapshotSkipsAFileItCannotReadAndSaysSo(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("notes", ideasManifest)
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
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 000")
	}
	if err := os.WriteFile(filepath.Join(data[0], "kept.txt"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Sweep()
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
	if row := e.WaitHealth("snapshot", "warning"); !strings.Contains(row.Error, "locked.bin") {
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
	e.Sweep()
	if row := e.WaitHealth("snapshot", "error"); row.Error == "" {
		t.Fatalf("a snapshot that fails says why in health: %+v", row)
	}
	_ = os.Remove(h.Backups())
	left := filepath.Join(h.Backups(), ".files-crashed")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(left, "7"), []byte("a file the owner deleted"), 0o600)
	e.Clock.Add(time.Hour)
	e.Sweep()
	e.WaitHealth("snapshot", "ok")
	if _, err := os.Stat(left); err == nil {
		t.Fatal("the links a snapshot cut short by a crash left go with the next snapshot")
	}
}

// TestSnapshotsKeepToTheirBudget: a snapshot leaves out the folders a data folder fills again and big data files past
// half the budget, saying how much in health, and the oldest snapshots go until all of them fit the budget.
func TestSnapshotsKeepToTheirBudget(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithConfig(func(c *app.Config) { c.SnapshotBudget = 4 << 20 }))
	h := deckhome.Layout(e.App.Cfg.Home)
	e.Project("notes", harness.PlainManifest)
	var id int64
	if err := e.App.Store.DB.QueryRow(`SELECT id FROM project WHERE name = 'notes'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	data := h.Data(id)
	random := make([]byte, 3<<20)
	_, _ = rand.Read(random)
	for name, body := range map[string][]byte{"keep.txt": []byte("kept"), ".cache/x.bin": random, "uv-cache/y": []byte("y"), "big.bin": random} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(data, name)), 0o700)
		if err := os.WriteFile(filepath.Join(data, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(h.Backups(), 0o700)
	old := filepath.Join(h.Backups(), "deck-20200101T000000.tar.gz")
	if err := os.WriteFile(old, make([]byte, 4<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	var out struct{ Path string }
	e.Must("deck.snapshot_create", nil, &out)
	paths := []string{out.Path}
	_, entries := snapshotFiles(t, paths[0])
	var kept []string
	for name := range entries {
		if strings.Contains(name, "/data/") {
			kept = append(kept, filepath.Base(name))
		}
	}
	if len(kept) != 1 || kept[0] != "keep.txt" {
		t.Fatalf("the snapshot keeps the data but caches and big files: %v", kept)
	}
	if row := e.WaitHealth("snapshot", "ok"); !strings.Contains(row.Error, "without 3 MB") {
		t.Fatalf("health says how much was left out: %+v", row)
	}
	left, _ := filepath.Glob(filepath.Join(h.Backups(), "*.tar.gz"))
	total := int64(0)
	for _, p := range left {
		fi, _ := os.Stat(p)
		total += fi.Size()
	}
	if total > 4<<20 || !slices.Contains(left, paths[0]) || slices.Contains(left, old) {
		t.Fatalf("the snapshots fit the budget and the newest stays: %v %d", left, total)
	}
}

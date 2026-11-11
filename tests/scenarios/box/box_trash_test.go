package box

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// swapLib swaps the folder data/sub and the link data/lnk into deck's state over and over, so a path deck checked
// may go through the link when deck moves the file.
const swapLib = harness.PythonLib + `
import ctypes, threading, time
inp = json.load(sys.stdin)
data = os.environ["DECK_DATA"]
sub, lnk = os.path.join(data, "sub"), os.path.join(data, "lnk")
os.makedirs(sub, exist_ok=True)
if not os.path.lexists(lnk):
    os.symlink("../../../../state/keys/1", lnk)
dfd = os.open(sub, os.O_RDONLY)
libc = ctypes.CDLL(None, use_errno=True)
stop = False
def swap():
    a, b = sub.encode(), lnk.encode()
    while not stop:
        if sys.platform == "darwin":
            libc.renamex_np(a, b, 0x2)
        else:
            libc.renameat2(-100, a, -100, b, 0x2)
def put(name, text):
    fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600, dir_fd=dfd)
    os.write(fd, text)
    os.close(fd)
def here(name):
    try:
        os.stat(name, dir_fd=dfd)
        return True
    except OSError:
        return False
deadline = time.time() + inp.get("n", 15)
`

// deleteRace deletes sub/openrouter while the folder swaps; a delete that answers yes and leaves the file in the
// real folder moved another file.
const deleteRace = swapLib + `
t = threading.Thread(target=swap, daemon=True)
t.start()
won, tries = 0, 0
while time.time() < deadline and not won:
    try:
        put("openrouter", b"decoy")
    except OSError:
        pass
    tries += 1
    st, out = deck("deck.file_delete", {"path": "sub/openrouter"})
    if st == 200 and here("openrouter"):
        won = out["file"]
stop = True
t.join()
print(json.dumps({"won": won, "tries": tries}))
`

// restoreRace deletes files of the real folder first, tells their ids in data/ids.json and swaps the folder while
// the owner brings them back to sub/.
const restoreRace = swapLib + `
ids = []
for i in range(inp.get("files", 60)):
    put("f%d" % i, b"planted")
    st, out = deck("deck.file_delete", {"path": "sub/f%d" % i})
    if st != 200:
        print(json.dumps({"error": "delete f%d: %s %s" % (i, st, out)}))
        sys.exit(1)
    ids.append([i, out["file"]])
t = threading.Thread(target=swap, daemon=True)
t.start()
with open(os.path.join(data, "ids.json"), "w") as f:
    json.dump(ids, f)
while time.time() < deadline:
    time.sleep(0.1)
stop = True
t.join()
print(json.dumps({"deleted": len(ids)}))
`

// relinkData tries to put a link into deck's state in place of the data folder itself, then deletes a file there.
const relinkData = harness.PythonLib + `
import shutil
data = os.environ["DECK_DATA"]
replaced = ""
try:
    shutil.rmtree(data)
    os.symlink(os.path.relpath(os.path.join(data, "..", "..", "..", "..", "state", "keys", "1"), os.path.dirname(data)), data)
    replaced = os.readlink(data)
except OSError as e:
    replaced = ""
st, out = deck("deck.file_delete", {"path": "openrouter"})
print(json.dumps({"replaced": replaced, "delete": st}))
`

func trashRace(t *testing.T, script string, owner func(e *harness.Env, data string)) (harness.Proc, string) {
	e := harness.New(t)
	keys := deckhome.Layout(e.App.Cfg.Home).Keys(1)
	if err := deckhome.WriteFile(filepath.Join(keys, "openrouter"), []byte("sk-or-VICTIM-KEY")); err != nil {
		t.Fatal(err)
	}
	e.Project("evil", harness.Manifest(`{"commands": [
    {"name": "race", "title": "Race", "description": "Races.", "class": "internal",
     "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "race"}}],
  "scripts": [{"name": "race", "file": "scripts/race.py", "runtime": "python"}]}`))
	e.SaveScript("evil", "scripts/race.py", script)
	id := e.Start("evil", "race", map[string]any{"n": 15})
	if owner != nil {
		owner(e, deckhome.Layout(e.App.Cfg.Home).Data(e.ProjectID("evil")))
	}
	p := e.WaitState(id, "done", "error")
	t.Logf("race: %s %s", p.State, p.Result)
	if p.State != "done" {
		t.Fatalf("the race script failed: %s", p.Error)
	}
	return p, keys
}

func TestAScriptRacingFileDeleteMovesNothingOutOfDecksState(t *testing.T) {
	p, keys := trashRace(t, deleteRace, nil)
	var res struct{ Won int64 }
	_ = json.Unmarshal(p.Result, &res)
	if res.Won != 0 {
		t.Errorf("deck moved another file than sub/openrouter into the trash of the project as file %d", res.Won)
	}
	if b, err := os.ReadFile(filepath.Join(keys, "openrouter")); err != nil || string(b) != "sk-or-VICTIM-KEY" {
		t.Errorf("the key of the connection left deck's state: %v", err)
	}
}

func TestAScriptRacingFileRestorePutsNothingIntoDecksState(t *testing.T) {
	restored := 0
	_, keys := trashRace(t, restoreRace, func(e *harness.Env, data string) {
		var ids [][2]int64
		e.Until("the script tells the files it deleted", func() bool {
			b, err := os.ReadFile(filepath.Join(data, "ids.json"))
			return err == nil && json.Unmarshal(b, &ids) == nil
		})
		for tries := 0; len(ids) > 0 && tries < 20000; tries++ {
			f := ids[tries%len(ids)]
			code, _ := e.Call("deck.file_restore", map[string]any{"file": f[1], "owner_kind": "data", "path": fmt.Sprintf("sub/f%d", f[0])})
			if code == 200 {
				restored++
				ids = slices.Delete(ids, tries%len(ids), tries%len(ids)+1)
			}
		}
	})
	t.Logf("the owner brought back %d files", restored)
	entries, _ := os.ReadDir(keys)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "f") {
			t.Errorf("deck put a file of the trash into deck's state: %s", filepath.Join(keys, e.Name()))
		}
	}
}

func TestAScriptCannotPutALinkInPlaceOfItsDataFolder(t *testing.T) {
	p, keys := trashRace(t, relinkData, nil)
	var res struct {
		Replaced string
		Delete   int
	}
	_ = json.Unmarshal(p.Result, &res)
	if res.Replaced != "" {
		t.Errorf("the script put a link to %s in place of its data folder", res.Replaced)
	}
	if b, err := os.ReadFile(filepath.Join(keys, "openrouter")); err != nil || string(b) != "sk-or-VICTIM-KEY" {
		t.Errorf("file_delete moved the key of the connection out of deck's state (%d): %v", res.Delete, err)
	}
}

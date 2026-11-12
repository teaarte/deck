package box

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"deck2/internal/app/documents"
	"deck2/internal/store/db"
	"deck2/master"
	"deck2/tests/harness"
)

// heapGuard stops the test binary when deck's heap passes 1 GB: a read of /dev/zero never ends by itself.
func heapGuard(t *testing.T) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			if runtime.ReadMemStats(&ms); ms.HeapAlloc > 1<<30 {
				panic(t.Name() + ": deck reads a link to /dev/zero into its memory, heap over 1 GB")
			}
		}
	}()
}

func TestALinkToDevZeroInARepoDoesNotEatDecksMemory(t *testing.T) {
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("a", harness.OnLogin(harness.TestManifest([]string{harness.FolderCommand("work", "true")}, nil)))
	folder := harness.RepoFolder(t)
	e.Must("deck.folder_add", map[string]any{"project": "a", "path": folder}, nil)
	if err := os.MkdirAll(filepath.Join(folder, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"settings.json", "settings.local.json"} {
		if err := os.Symlink("/dev/zero", filepath.Join(folder, ".claude", f)); err != nil {
			t.Fatal(err)
		}
	}
	heapGuard(t)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Errorf("the turn in a repo whose settings link to /dev/zero ended %s: %s", got.State, got.Error)
	}
}

func TestALinkToDevZeroInTheMastersDataDoesNotEatDecksMemory(t *testing.T) {
	e := harness.New(t)
	mp, err := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: master.Name})
	if err != nil {
		t.Fatal(err)
	}
	dir := e.App.Home.Data(mp.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	pic := filepath.Join(dir, documents.MapPicture)
	if err := os.Symlink("/dev/zero", pic); err != nil {
		t.Fatal(err)
	}
	heapGuard(t)
	e.Must("deck.memory_map", map[string]any{}, nil)
	if fi, err := os.Lstat(pic); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("memory_map left no picture of its own in place of the link: %v", err)
	}
}

package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestAProcessDeletesOnlyTheFilesOfItsOwnRun: an agent sent back for rework deletes the drafts its run made, as one
// line of done without you that brings them back; a file of another run stays.
func TestAProcessDeletesOnlyTheFilesOfItsOwnRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if _, ids, ok := strings.Cut(c.Req.Prompt, "delete files "); ok {
			var outs []string
			for _, id := range strings.Fields(strings.SplitN(ids, "\n", 2)[0]) {
				n, _ := strconv.ParseInt(strings.TrimFunc(id, func(r rune) bool { return r < '0' || r > '9' }), 10, 64)
				out, _ := c.Tool(ctx, "file_delete", map[string]any{"file": n})
				outs = append(outs, out)
			}
			return provider.Result{Text: strings.Join(outs, "\n")}
		}
		results := filepath.Join(c.Req.Cwd, "..", "results")
		for _, n := range []string{"draft-1.png", "draft-2.png", "final.png"} {
			_ = os.WriteFile(filepath.Join(results, n), []byte("png"), 0o600)
		}
		return provider.Result{Text: "drawn"}
	})
	p := e.createProject("draw", plainManifest)
	mine, other := e.run(p, "work"), e.run(p, "work")
	e.waitState(mine, "done")
	e.waitState(other, "done")
	files := func(process int64) map[string]int64 {
		var fs []struct {
			ID   int64
			Name string
		}
		e.must("deck.files_list", map[string]any{"process": process}, &fs)
		out := map[string]int64{}
		for _, f := range fs {
			out[f.Name] = f.ID
		}
		return out
	}
	m, o := files(mine), files(other)
	if len(m) != 3 || len(o) != 3 {
		t.Fatalf("each run keeps its three files: %v %v", m, o)
	}
	e.must("deck.process_rework", map[string]any{"process": mine, "remark": fmt.Sprintf("delete files %d %d %d", m["draft-1.png"], m["draft-2.png"], o["draft-1.png"])}, nil)
	e.waitFor("the rework done", func() bool {
		p := e.process(mine)
		return p.State == "done" && strings.Contains(string(p.Result), "file")
	})
	if said := string(e.process(mine).Result); !strings.Contains(said, "deletes only the files its own run made") {
		t.Fatalf("a file of another run is refused: %s", said)
	}
	if got, kept := files(mine), files(other); len(got) != 1 || got["final.png"] == 0 || len(kept) != 3 {
		t.Fatalf("the drafts of its own run are gone, the rest stays: %v %v", got, kept)
	}
	var feed []struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "deleted files: 2") {
		t.Fatalf("the deletions of the run are one line of done without you with an undo: %+v", feed)
	}
	e.must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := files(mine); len(got) != 3 {
		t.Fatalf("the undo brings both back: %v", got)
	}
}

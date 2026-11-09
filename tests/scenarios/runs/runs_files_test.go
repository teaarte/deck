package runs

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAProcessDeletesOnlyTheFilesOfItsOwnRun: an agent sent back for rework deletes the drafts its run made, as one
// line of done without you that brings them back; a file of another run stays.
func TestAProcessDeletesOnlyTheFilesOfItsOwnRun(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
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
	}))

	p := e.Project("draw", harness.PlainManifest)
	mine, other := e.Run(p, "work"), e.Run(p, "work")
	e.WaitState(mine, "done")
	e.WaitState(other, "done")
	files := func(process int64) map[string]int64 {
		var fs []struct {
			ID   int64
			Name string
		}
		e.Must("deck.files_list", map[string]any{"process": process}, &fs)
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
	e.Must("deck.process_rework", map[string]any{"process": mine, "remark": fmt.Sprintf("delete files %d %d %d", m["draft-1.png"], m["draft-2.png"], o["draft-1.png"])}, nil)
	e.Until("the rework done", func() bool {
		p := e.Proc(mine)
		return p.State == "done" && strings.Contains(string(p.Result), "file")
	})
	if said := string(e.Proc(mine).Result); !strings.Contains(said, "deletes only the files its own run made") {
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
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "deleted files: 2") {
		t.Fatalf("the deletions of the run are one line of done without you with an undo: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := files(mine); len(got) != 3 {
		t.Fatalf("the undo brings both back: %v", got)
	}
}

// TestAnAgentSeesAPictureOfItsDataFolder: data_read gives a picture of the module's own data folder to its agent as an
// image; a picture over the limit is refused with its size, one of another project is refused, .. stays inside.
func TestAnAgentSeesAPictureOfItsDataFolder(t *testing.T) {
	t.Parallel()
	picture := func(w, h int) []byte {
		var b bytes.Buffer
		_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)))
		return b.Bytes()
	}
	plan := picture(40, 30)
	got := make(chan []*mcp.CallToolResult, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var outs []*mcp.CallToolResult
		for _, in := range []map[string]any{
			{"project": "draw", "path": "dom/16/plan.png"}, {"project": "draw", "path": "big.png"}, {"project": "house", "path": "plan.png"},
			{"project": "draw", "path": "../secret.png"}, {"project": "draw", "path": "wide.png"},
			{"project": "draw", "path": "dom/16/plan.png", "fields": []string{"name"}},
		} {
			res, err := c.Session.CallTool(ctx, &mcp.CallToolParams{Name: "data_read", Arguments: in})
			if err != nil {
				res = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
			}
			outs = append(outs, res)
		}
		got <- outs
		return provider.Result{Text: "seen"}
	}))
	p, house := e.Project("draw", harness.PlainManifest), e.Project("house", harness.PlainManifest)
	data := e.App.Home.Data(p)
	_ = os.MkdirAll(filepath.Join(data, "dom", "16"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "dom", "16", "plan.png"), plan, 0o600)
	_ = os.WriteFile(filepath.Join(data, "big.png"), append(plan, make([]byte, 4<<20)...), 0o600)
	_ = os.WriteFile(filepath.Join(data, "wide.png"), picture(8001, 1), 0o600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(data), "secret.png"), plan, 0o600)
	_ = os.MkdirAll(e.App.Home.Data(house), 0o700)
	_ = os.WriteFile(filepath.Join(e.App.Home.Data(house), "plan.png"), plan, 0o600)
	e.WaitState(e.Run(p, "work"), "done")
	outs := <-got
	text := func(r *mcp.CallToolResult) string {
		var b strings.Builder
		for _, c := range r.Content {
			if t, ok := c.(*mcp.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	}
	own := outs[0]
	if own.IsError || len(own.Content) != 2 || !strings.Contains(text(own), `"url":"/api/files/data/draw/dom/16/plan.png"`) {
		t.Fatalf("the picture comes with its address: %v %s", own.IsError, text(own))
	}
	if img, ok := own.Content[1].(*mcp.ImageContent); !ok || img.MIMEType != "image/png" || !bytes.Equal(img.Data, plan) {
		t.Fatalf("the picture comes as an image of its bytes: %#v", own.Content[1])
	}
	if big := outs[1]; !big.IsError || !strings.Contains(text(big), strconv.Itoa(len(plan)+4<<20)) || !strings.Contains(text(big), "3932160") {
		t.Fatalf("a picture over the limit is refused with its size and the limit: %s", text(big))
	}
	if other := outs[2]; !other.IsError || len(other.Content) != 1 || !strings.Contains(text(other), "another project") {
		t.Fatalf("a picture of another project is refused: %s", text(other))
	}
	if out := outs[3]; !out.IsError || len(out.Content) != 1 || !strings.Contains(text(out), "no \"../secret.png\"") {
		t.Fatalf("nothing outside the data folder is read: %s", text(out))
	}
	if wide := outs[4]; !wide.IsError || !strings.Contains(text(wide), "8001×1") || !strings.Contains(text(wide), "8000") {
		t.Fatalf("a picture over the side limit is refused with its size and the limit: %s", text(wide))
	}
	if cut := outs[5]; cut.IsError || len(cut.Content) != 2 {
		t.Fatalf("a read of some fields keeps the picture: %s", text(cut))
	}
	var owner struct {
		Image struct {
			Type, URL     string
			Width, Height int
		}
	}
	e.Must("deck.data_read", map[string]any{"project": "draw", "path": "dom/16/plan.png"}, &owner)
	if owner.Image.Type != "image/png" || owner.Image.URL != "/api/files/data/draw/dom/16/plan.png" || owner.Image.Width != 40 || owner.Image.Height != 30 {
		t.Fatalf("the owner's API names the picture by its address: %+v", owner)
	}
	_ = os.WriteFile(filepath.Join(data, "plan.pdf"), []byte("%PDF-1.7\n"), 0o600)
	if code, out := e.Call("deck.data_read", map[string]any{"project": "draw", "path": "plan.pdf"}); code == 200 || !strings.Contains(string(out), "PDF") {
		t.Fatalf("a PDF is refused with how to read it: %d %s", code, out)
	}
}

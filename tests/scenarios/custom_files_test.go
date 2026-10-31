package scenarios

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
)

// TestACustomBlockReadsOnlyTheFilesOfItsModule: the host of a custom block reads a file of the module of its screen,
// stored or in its data folder, whole; a file of another module, a path out of the data folder or one too big is refused.
func TestACustomBlockReadsOnlyTheFilesOfItsModule(t *testing.T) {
	t.Parallel()
	e := New(t)
	p := e.Project("web", webNotes)
	e.Project("other", plainManifest)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "pics"), 0o700)
	png := "\x89PNG\r\n\x1a\nfake"
	_ = os.WriteFile(filepath.Join(data, "pics", "a b.png"), []byte(png), 0o600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(data), "secret.txt"), []byte("outside"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "big.mp4"), nil, 0o600)
	_ = os.Truncate(filepath.Join(data, "big.mp4"), 201<<20)
	own := e.upload("web", "logo.svg", "image/svg+xml", "<svg/>")
	theirs := e.upload("other", "theirs.txt", "text/plain", "theirs")

	block := blockData[map[string]any](t, e.screenData("web", "main", nil)["b2"]["value"])
	if block["project"] != "web" {
		t.Fatalf("a custom block knows the module it reads files of: %+v", block["project"])
	}
	type read struct {
		URI, Name, Type, Blob string
		Size                  int64
	}
	var got read
	e.Must("deck.file_read", map[string]any{"project": "web", "uri": "deck://data/pics/a%20b.png"}, &got)
	if b, _ := base64.StdEncoding.DecodeString(got.Blob); string(b) != png || got.Type != "image/png" || got.Name != "a b.png" || got.URI != "deck://data/pics/a%20b.png" {
		t.Fatalf("a file of the data folder is read whole with its type: %+v", got)
	}
	e.Must("deck.file_read", map[string]any{"project": "web", "uri": fmt.Sprintf("deck://file/%d", own)}, &got)
	if b, _ := base64.StdEncoding.DecodeString(got.Blob); string(b) != "<svg/>" || got.Type != "image/svg+xml" || got.Name != "logo.svg" {
		t.Fatalf("a stored file of the module is read whole with its type: %+v", got)
	}
	for _, uri := range []string{fmt.Sprintf("deck://file/%d", theirs), "deck://data/../secret.txt", "deck://data/%2e%2e/secret.txt",
		"deck://data/pics/../../secret.txt", "deck://data/", "https://example.com/a.png", "deck://file/x", "/api/files/1"} {
		if code, out := e.Call("deck.file_read", map[string]any{"project": "web", "uri": uri}); code == 200 || strings.Contains(string(out), "outside") || strings.Contains(string(out), "dGhlaXJz") {
			t.Fatalf("%s is not a file of the module: %d %s", uri, code, out)
		}
	}
	if code, out := e.Call("deck.file_read", map[string]any{"project": "web", "uri": "deck://data/big.mp4"}); code == 200 || !strings.Contains(string(out), "200 MB") {
		t.Fatalf("a file over 200 MB is refused with why: %d %s", code, out)
	}
	if code, _ := e.Call("deck.file_read", map[string]any{"project": "other", "uri": fmt.Sprintf("deck://file/%d", theirs)}); code != 200 {
		t.Fatalf("the other module reads its own file: %d", code)
	}
}

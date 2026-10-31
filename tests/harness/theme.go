package harness

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

type HostTheme struct{ Theme, Bg, Ring, Radius string }

// Example saves a theme of the master's skill of themes as the owner's, as Deki does when asked for that look.
func (e *Env) Example(name string) {
	e.T.Helper()
	raw, err := os.ReadFile(filepath.Join(Root, "master", "skills", "themes", "examples", name+".json"))
	if err != nil {
		e.T.Fatal(err)
	}
	e.Must("deck.theme_save", map[string]any{"name": name, "text": string(raw)}, nil)
}

func Solid(c color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.String()
}

type ThemeRow struct {
	Name     string
	Title    string
	Current  bool
	Builtin  bool
	Own      bool
	Problems []string
}

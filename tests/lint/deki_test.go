package lint

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/image/webp"
)

// translucent says how much of the inside of a picture lets the background through: its pixels neither empty nor
// solid, but for the edge of the silhouette, three pixels deep.
func translucent(img image.Image) float64 {
	b := img.Bounds()
	alpha := func(x, y int) uint32 {
		if !(image.Point{x, y}).In(b) {
			return 0
		}
		_, _, _, a := img.At(x, y).RGBA()
		return a >> 8
	}
	inside, soft := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if alpha(x, y) == 0 {
				continue
			}
			edge := false
			for dy := -3; dy <= 3 && !edge; dy++ {
				for dx := -3; dx <= 3 && !edge; dx++ {
					edge = alpha(x+dx, y+dy) == 0
				}
			}
			if edge {
				continue
			}
			inside++
			if alpha(x, y) < 250 {
				soft++
			}
		}
	}
	return float64(soft) / float64(max(inside, 1))
}

// TestDekiIsOpaque: every picture of Deki is solid inside its silhouette; only the first set, listed in
// deki_translucent.txt, may not be, and leaves the list as the master draws it anew.
func TestDekiIsOpaque(t *testing.T) {
	raw, err := os.ReadFile("deki_translucent.txt")
	if err != nil {
		t.Fatal(err)
	}
	known := strings.Fields(string(raw))
	files, err := filepath.Glob("../../web/public/deki/*.webp")
	if err != nil || len(files) == 0 {
		t.Fatalf("no pictures of Deki: %v", err)
	}
	var seen []string
	for _, f := range files {
		name := filepath.Base(f)
		seen = append(seen, name)
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		img, err := webp.Decode(r)
		r.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		share := translucent(img)
		switch listed := slices.Contains(known, name); {
		case share > 0.005 && !listed:
			t.Errorf("%s lets the background through: %.1f%% of its inside is not solid", name, 100*share)
		case share <= 0.005 && listed:
			t.Errorf("%s is solid now: take it off deki_translucent.txt", name)
		}
	}
	for _, name := range known {
		if !slices.Contains(seen, name) {
			t.Errorf("%s is gone: take it off deki_translucent.txt", name)
		}
	}
}

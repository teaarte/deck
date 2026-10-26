package lint

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/webp"
)

// translucent says how much of the inside of a picture lets the background through: its pixels neither empty nor
// solid, but for the soft edge of the silhouette, three pixels in from where it is more see-through than not.
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
					edge = alpha(x+dx, y+dy) < 128
				}
			}
			if edge {
				continue
			}
			inside++
			if alpha(x, y) < 255 {
				soft++
			}
		}
	}
	return float64(soft) / float64(max(inside, 1))
}

// TestDekiIsOpaque: every picture of Deki is solid inside its silhouette.
func TestDekiIsOpaque(t *testing.T) {
	files, err := filepath.Glob("../../web/public/deki/*.webp")
	if err != nil || len(files) == 0 {
		t.Fatalf("no pictures of Deki: %v", err)
	}
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		img, err := webp.Decode(r)
		r.Close()
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		if share := translucent(img); share > 0.005 {
			t.Errorf("%s lets the background through: %.1f%% of its inside is not solid", filepath.Base(f), 100*share)
		}
	}
}

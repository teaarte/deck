package files

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"

	"deck2/internal/store/db"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ThumbWidth is the width of the light copy of a picture: twice a card.
const ThumbWidth = 720

// maxPixels is the largest picture deck makes a light copy of; a larger one is shown itself.
const maxPixels = 50_000_000

// Light is the light copy of a picture as JPEG, no wider than ThumbWidth, with the size of the picture and its main
// color as #rrggbb.
type Light struct {
	JPEG          []byte
	Width, Height int
	Color         string
}

// Thumb is the light copy of a picture.
func Thumb(r io.ReadSeeker) (Light, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return Light{}, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return Light{}, fmt.Errorf("deck: a picture of %d×%d is too large for a light copy", cfg.Width, cfg.Height)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return Light{}, err
	}
	src, _, err := image.Decode(r)
	if err != nil {
		return Light{}, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return Light{}, fmt.Errorf("deck: an empty picture")
	}
	out := Light{Width: w, Height: h}
	if w > ThumbWidth {
		w, h = ThumbWidth, max(1, h*ThumbWidth/w)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	dot := image.NewRGBA(image.Rect(0, 0, 1, 1))
	draw.CatmullRom.Scale(dot, dot.Bounds(), dst, dst.Bounds(), draw.Src, nil)
	c := color.RGBAModel.Convert(dot.At(0, 0)).(color.RGBA)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return Light{}, err
	}
	out.JPEG, out.Color = buf.Bytes(), fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	return out, nil
}

// Size is the size of a picture by its header, zero when it is none.
func Size(r io.Reader) (int, int) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// Seen is how a stored file shows small: the address of its light copy, with the size and main color of the picture;
// empty for a file that is no picture or has no light copy yet.
type Seen struct {
	Thumb  string `json:"thumb,omitempty"`
	Width  int64  `json:"width,omitempty"`
	Height int64  `json:"height,omitempty"`
	Color  string `json:"color,omitempty"`
}

// SeenOf is how a stored file shows small.
func SeenOf(f db.File) Seen {
	if !f.ThumbID.Valid || f.ThumbID.Int64 == 0 {
		return Seen{}
	}
	return Seen{Thumb: fmt.Sprintf("/api/files/%d", f.ThumbID.Int64), Width: f.Width, Height: f.Height, Color: f.Color}
}

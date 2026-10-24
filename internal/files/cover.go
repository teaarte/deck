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
	"slices"

	"deck2/internal/store/db"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// CoverSlot is the slot of the picture of a project's card; PictureKind the owner kind of the files of a picture.
const (
	CoverSlot   = "cover"
	PictureKind = "picture"
)

// Pick is the picture a theme and scheme show: of the theme and scheme, of the theme in the other scheme, the one for
// every theme in the scheme or the other, then of any theme in the scheme or any at all; nil without one.
func Pick(pics []db.Picture, theme, scheme string) *db.Picture {
	other := "light"
	if scheme != "dark" {
		other = "dark"
	}
	for _, want := range [][2]string{{theme, scheme}, {theme, other}, {"", scheme}, {"", other}, {"*", scheme}, {"*", ""}} {
		if i := slices.IndexFunc(pics, func(p db.Picture) bool {
			return (want[0] == "*" || p.Theme == want[0]) && (want[1] == "" || p.Scheme == want[1])
		}); i >= 0 {
			return &pics[i]
		}
	}
	return nil
}

// Shown is the file a card reads of a picture: its light copy, or the picture itself without one.
func Shown(p db.Picture) int64 {
	if p.ThumbID.Valid && p.ThumbID.Int64 != 0 {
		return p.ThumbID.Int64
	}
	return p.FileID
}

// ThumbWidth is the width of the light copy of a picture: twice a card.
const ThumbWidth = 720

// maxPixels is the largest picture deck makes a light copy of; a larger one is shown itself.
const maxPixels = 50_000_000

// Thumb is the light copy of a picture as JPEG, no wider than ThumbWidth, and its main color as #rrggbb.
func Thumb(r io.ReadSeeker) ([]byte, string, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, "", err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, "", fmt.Errorf("deck: a picture of %d×%d is too large for a light copy", cfg.Width, cfg.Height)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, "", err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, "", fmt.Errorf("deck: an empty picture")
	}
	if w > ThumbWidth {
		w, h = ThumbWidth, max(1, h*ThumbWidth/w)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	dot := image.NewRGBA(image.Rect(0, 0, 1, 1))
	draw.CatmullRom.Scale(dot, dot.Bounds(), dst, dst.Bounds(), draw.Src, nil)
	c := color.RGBAModel.Convert(dot.At(0, 0)).(color.RGBA)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, "", err
	}
	return out.Bytes(), fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B), nil
}

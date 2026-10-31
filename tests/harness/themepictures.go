package harness

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

func Solid(c color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.String()
}

package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// rgba is a color with straight alpha, channels 0..1.
type rgba struct{ r, g, b, a float64 }

func (c rgba) over(bg rgba) rgba {
	a := c.a + bg.a*(1-c.a)
	if a == 0 {
		return rgba{}
	}
	mix := func(x, y float64) float64 { return (x*c.a + y*bg.a*(1-c.a)) / a }
	return rgba{mix(c.r, bg.r), mix(c.g, bg.g), mix(c.b, bg.b), a}
}

func (c rgba) luminance() float64 {
	lin := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// contrast is the WCAG ratio of text fg on an opaque bg.
func contrast(fg, bg rgba) float64 {
	a, b := fg.over(bg).luminance(), bg.luminance()
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func hexColor(s string) (rgba, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 || len(s) == 4 {
		var b strings.Builder
		for _, c := range s {
			b.WriteRune(c)
			b.WriteRune(c)
		}
		s = b.String()
	}
	if len(s) != 6 && len(s) != 8 {
		return rgba{}, false
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgba{}, false
	}
	if len(s) == 6 {
		n = n<<8 | 0xff
	}
	return rgba{float64(n>>24&0xff) / 255, float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255}, true
}

// args splits the inside of a function at its top-level commas.
func args(s string) []string {
	var out []string
	depth, from := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[from:i]))
				from = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[from:]))
}

// fn splits name(inside) into its name and inside.
func fn(s string) (string, string, bool) {
	i := strings.IndexByte(s, '(')
	if i <= 0 || !strings.HasSuffix(s, ")") {
		return "", "", false
	}
	return strings.ToLower(s[:i]), s[i+1 : len(s)-1], true
}

func number(s string, scale float64) (float64, error) {
	if p, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(p, 64)
		return v / 100, err
	}
	v, err := strconv.ParseFloat(s, 64)
	return v / scale, err
}

// colors resolves values of tokens to colors: the palette, the accent and the tokens of one scheme.
type colors struct {
	tokens map[string]string
	accent string
	depth  int
}

func (cs *colors) color(v string) (rgba, error) {
	v = strings.TrimSpace(v)
	switch strings.ToLower(v) {
	case "transparent":
		return rgba{}, nil
	case "white":
		return rgba{1, 1, 1, 1}, nil
	case "black":
		return rgba{0, 0, 0, 1}, nil
	}
	if strings.HasPrefix(v, "#") {
		if c, ok := hexColor(v); ok {
			return c, nil
		}
		return rgba{}, fmt.Errorf("%s is no color", v)
	}
	name, in, ok := fn(v)
	if !ok {
		return rgba{}, fmt.Errorf("%s is no color deck can read: give #hex, rgb() or color-mix()", v)
	}
	switch name {
	case "var":
		return cs.variable(strings.TrimSpace(in))
	case "rgb", "rgba":
		return rgbColor(in)
	case "color-mix":
		return cs.mix(in)
	}
	return rgba{}, fmt.Errorf("%s is no color deck can read: give #hex, rgb() or color-mix()", v)
}

func rgbColor(in string) (rgba, error) {
	in = strings.NewReplacer(",", " ", "/", " ").Replace(in)
	f := strings.Fields(in)
	if len(f) != 3 && len(f) != 4 {
		return rgba{}, fmt.Errorf("rgb(%s) takes three channels and an alpha", in)
	}
	var c [4]float64
	c[3] = 1
	for i, s := range f {
		scale := 255.0
		if i == 3 {
			scale = 1
		}
		v, err := number(s, scale)
		if err != nil {
			return rgba{}, fmt.Errorf("rgb(%s): %s is no number", in, s)
		}
		c[i] = min(max(v, 0), 1)
	}
	return rgba{c[0], c[1], c[2], c[3]}, nil
}

// mix is color-mix(in srgb, a p%, b q%) in premultiplied sRGB, as browsers do it.
func (cs *colors) mix(in string) (rgba, error) {
	a := args(in)
	if len(a) != 3 || strings.ToLower(strings.Join(strings.Fields(a[0]), " ")) != "in srgb" {
		return rgba{}, fmt.Errorf("color-mix(%s): deck reads color-mix(in srgb, a p%%, b)", in)
	}
	part := func(s string) (rgba, float64, bool, error) {
		f := strings.Fields(s)
		if n := len(f); n > 1 && strings.HasSuffix(f[n-1], "%") {
			p, err := number(f[n-1], 1)
			if err != nil {
				return rgba{}, 0, false, err
			}
			c, err := cs.color(strings.Join(f[:n-1], " "))
			return c, p, true, err
		}
		c, err := cs.color(s)
		return c, 0, false, err
	}
	c1, p1, has1, err := part(a[1])
	if err != nil {
		return rgba{}, err
	}
	c2, p2, has2, err := part(a[2])
	if err != nil {
		return rgba{}, err
	}
	switch {
	case has1 && !has2:
		p2 = 1 - p1
	case !has1 && has2:
		p1 = 1 - p2
	case !has1 && !has2:
		p1, p2 = 0.5, 0.5
	}
	if sum := p1 + p2; sum > 0 {
		p1, p2 = p1/sum, p2/sum
	}
	al := c1.a*p1 + c2.a*p2
	if al == 0 {
		return rgba{}, nil
	}
	ch := func(x, y float64) float64 { return (x*c1.a*p1 + y*c2.a*p2) / al }
	return rgba{ch(c1.r, c2.r), ch(c1.g, c2.g), ch(c1.b, c2.b), al}, nil
}

func (cs *colors) variable(name string) (rgba, error) {
	if cs.depth > 8 {
		return rgba{}, fmt.Errorf("var(%s) refers to itself", name)
	}
	n := strings.TrimPrefix(name, "--")
	if v, ok := cs.tokens[n]; ok {
		cs.depth++
		defer func() { cs.depth-- }()
		return cs.color(v)
	}
	if n == "accent" {
		return shade(cs.accent, 6)
	}
	if rest, ok := strings.CutPrefix(n, "accent-"); ok {
		if k, err := strconv.Atoi(rest); err == nil {
			return shade(cs.accent, k)
		}
	}
	if rest, ok := strings.CutPrefix(n, "color-"); ok {
		if i := strings.LastIndexByte(rest, '-'); i > 0 {
			if k, err := strconv.Atoi(rest[i+1:]); err == nil {
				return shade(rest[:i], k)
			}
		}
	}
	return rgba{}, fmt.Errorf("var(%s) is no color deck can read", name)
}

func shade(name string, k int) (rgba, error) {
	p, ok := Palette.Colors[name]
	if !ok || k < 0 || k >= len(p) {
		return rgba{}, fmt.Errorf("no color %s-%d in the palette", name, k)
	}
	c, _ := hexColor(p[k])
	return c, nil
}

// stops are the colors of a value: the color itself, or the stops of a gradient.
func (cs *colors) stops(v string) ([]rgba, error) {
	name, in, ok := fn(strings.TrimSpace(v))
	if !ok || !strings.HasSuffix(name, "-gradient") {
		c, err := cs.color(v)
		return []rgba{c}, err
	}
	var out []rgba
	for _, a := range args(in) {
		f := strings.Fields(a)
		for len(f) > 1 {
			last := f[len(f)-1]
			if !strings.ContainsAny(last[:1], "0123456789.-") || strings.ContainsAny(last, "()") {
				break
			}
			f = f[:len(f)-1]
		}
		c, err := cs.color(strings.Join(f, " "))
		if err == nil {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no colors deck can read", v)
	}
	return out, nil
}

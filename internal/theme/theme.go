// Package theme reads the themes of deck: documents of tokens, parts and limited CSS checked before they are saved.
package theme

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/schemas"
)

// Theme is a theme as its document holds it.
type Theme struct {
	Version int                          `json:"version"`
	Title   map[string]string            `json:"title"`
	Mood    string                       `json:"mood"`
	Schemes []string                     `json:"schemes,omitempty"`
	Accent  string                       `json:"accent,omitempty"`
	Font    Font                         `json:"font"`
	Radius  string                       `json:"radius,omitempty"`
	Density string                       `json:"density,omitempty"`
	Tokens  map[string]string            `json:"tokens,omitempty"`
	Light   map[string]string            `json:"light,omitempty"`
	Dark    map[string]string            `json:"dark,omitempty"`
	Parts   map[string]map[string]string `json:"parts,omitempty"`
	CSS     string                       `json:"css,omitempty"`
}

type Font struct {
	Text     string `json:"text,omitempty"`
	Headings string `json:"headings,omitempty"`
}

// Look is a theme as the client applies it: the CSS of its tokens, parts and own rules, and what Mantine takes.
type Look struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Mood    string   `json:"mood"`
	Schemes []string `json:"schemes"`
	Accent  string   `json:"accent,omitempty"`
	Font    Font     `json:"font"`
	Radius  string   `json:"radius"`
	Density string   `json:"density"`
	CSS     string   `json:"css"`
	// Images are the pictures of the theme by slot, as covers are: the cockpit, the backdrop and the pattern.
	Images map[string]any `json:"images"`
	// Problems are what a check of today refuses in it; the client gets the theme without those parts.
	Problems []string `json:"problems,omitempty"`
}

type tokenDef struct {
	Var   string `json:"var"`
	Desc  string `json:"desc"`
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

type partDef struct {
	Selector string `json:"selector"`
	Radius   string `json:"radius"`
}

// Catalog is what a theme may name: tokens, variables, classes and parts.
var Catalog struct {
	Tokens  map[string]tokenDef `json:"tokens"`
	Vars    map[string]string   `json:"vars"`
	Classes map[string]string   `json:"classes"`
	Parts   map[string]partDef  `json:"parts"`
}

// Palette is deck's colors and the accents a device picks from.
var Palette struct {
	Colors  map[string][]string `json:"colors"`
	Accents []string            `json:"accents"`
}

var schema = func() *jsonschema.Schema {
	var raw struct {
		Catalog json.RawMessage `json:"catalog"`
	}
	if err := json.Unmarshal(schemas.Theme, &raw); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(raw.Catalog, &Catalog); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(schemas.Palette, &Palette); err != nil {
		panic(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.Theme))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://deck-theme.json", doc); err != nil {
		panic(err)
	}
	return c.MustCompile("mem://deck-theme.json")
}()

// Default is the theme a deck wears until the owner picks another.
const Default = "juce"

var nameRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// CheckName refuses a name a theme cannot have.
func CheckName(name string) error {
	if !nameRule.MatchString(name) {
		return fmt.Errorf("a theme name is lowercase letters, digits and -, up to 40: %q", name)
	}
	return nil
}

// DocName is the name of the document of a theme.
func DocName(name string) string { return "themes/" + name + ".json" }

// NameOf is the theme a document name holds, empty for another document.
func NameOf(doc string) string {
	if n, ok := strings.CutPrefix(doc, "themes/"); ok {
		if n, ok := strings.CutSuffix(n, ".json"); ok && nameRule.MatchString(n) {
			return n
		}
	}
	return ""
}

//go:embed shipped
var shipped embed.FS

// Slots are the pictures a theme may have: the cockpit's card, the page behind and a tile repeated over it.
var Slots = []string{"cockpit", "backdrop", "pattern"}

// Shipped is a picture deck ships with a theme, <slot>-<scheme>.webp, nil for none.
func Shipped(name, file string) []byte {
	if nameRule.MatchString(name) && path.Base(file) == file && strings.HasSuffix(file, ".webp") {
		if b, err := shipped.ReadFile("shipped/" + name + "/" + file); err == nil {
			return b
		}
	}
	return nil
}

// Builtin is the text of a theme deck ships, nil for none.
func Builtin(name string) []byte {
	b, err := shipped.ReadFile("shipped/" + name + ".json")
	if err != nil {
		return nil
	}
	return b
}

// Builtins are the names of the themes deck ships.
func Builtins() []string {
	es, _ := shipped.ReadDir("shipped")
	var out []string
	for _, e := range es {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			out = append(out, n)
		}
	}
	return out
}

// Parse reads a theme by its schema; a theme that fails it cannot be worn.
func Parse(raw []byte) (*Theme, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("theme: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		return nil, fmt.Errorf("theme does not match its schema: %w", err)
	}
	var t Theme
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("theme: %w", err)
	}
	if len(t.Schemes) == 0 {
		t.Schemes = []string{"light", "dark"}
	}
	return &t, nil
}

// tokens are the tokens of a scheme: the defaults, then the theme's for both, then its own for the scheme.
func (t *Theme) tokens(scheme string) map[string]string {
	out := map[string]string{}
	for n, d := range Catalog.Tokens {
		out[n] = d.Light
		if scheme == "dark" {
			out[n] = d.Dark
		}
	}
	maps.Copy(out, t.Tokens)
	if scheme == "dark" {
		maps.Copy(out, t.Dark)
	} else {
		maps.Copy(out, t.Light)
	}
	return out
}

var partProps = map[string]string{"shadow": "box-shadow", "border": "border", "background": "background", "transform": "text-transform",
	"weight": "font-weight", "spacing": "letter-spacing"}

func radius(v string) string {
	switch v {
	case "xs", "sm", "md", "lg", "xl":
		return "var(--mantine-radius-" + v + ")"
	}
	return v
}

// Wear is the look of a theme; problems name what deck's checks refuse in it, left out of the look.
func (t *Theme) Wear(name, lang string) Look {
	l := Look{Name: name, Title: t.Title["en"], Mood: t.Mood, Schemes: t.Schemes, Accent: t.Accent, Font: t.Font,
		Radius: cmpOr(t.Radius, "md"), Density: cmpOr(t.Density, "default"), Images: map[string]any{}}
	if s := t.Title[lang]; s != "" {
		l.Title = s
	}
	if !slices.Contains(Palette.Accents, t.Accent) && t.Accent != "" {
		l.Problems = append(l.Problems, fmt.Sprintf("accent: no accent %s; the accents are %s", t.Accent, strings.Join(Palette.Accents, ", ")))
		l.Accent = ""
	}
	for _, f := range []*string{&l.Font.Text, &l.Font.Headings} {
		if *f == "" {
			continue
		}
		v, err := Value(*f)
		if err != nil || strings.ContainsAny(*f, "(;{}") {
			l.Problems = append(l.Problems, fmt.Sprintf("font: %q is no font stack", *f))
			*f = ""
			continue
		}
		*f = v
	}
	var b strings.Builder
	for _, scheme := range t.Schemes {
		toks := t.tokens(scheme)
		b.WriteString(roots[scheme] + " {\n")
		for _, n := range sorted(toks) {
			if toks[n] == "" {
				continue
			}
			v, err := Value(toks[n])
			if err != nil {
				l.Problems = append(l.Problems, fmt.Sprintf("%s: %s: %v", scheme, n, err))
				continue
			}
			fmt.Fprintf(&b, "  %s: %s;\n", Catalog.Tokens[n].Var, v)
		}
		b.WriteString("}\n")
	}
	for _, p := range sorted(t.Parts) {
		def := Catalog.Parts[p]
		var rules []string
		for _, k := range sorted(t.Parts[p]) {
			v, err := Value(t.Parts[p][k])
			if err != nil {
				l.Problems = append(l.Problems, fmt.Sprintf("parts: %s.%s: %v", p, k, err))
				continue
			}
			switch {
			case k == "radius" && def.Radius != "":
				fmt.Fprintf(&b, "%s { %s: %s; }\n", Root, def.Radius, radius(v))
			case k == "radius":
				rules = append(rules, "border-radius: "+radius(v))
			default:
				rules = append(rules, partProps[k]+": "+v)
			}
		}
		if len(rules) > 0 {
			fmt.Fprintf(&b, "%s %s { %s; }\n", Root, def.Selector, strings.Join(rules, "; "))
		}
	}
	own, problems := Compile(t.CSS)
	b.WriteString(own)
	l.CSS = b.String()
	l.Problems = append(l.Problems, problems...)
	l.Problems = append(l.Problems, t.Contrast()...)
	return l
}

func cmpOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func sorted[V any](m map[string]V) []string {
	out := slices.Collect(maps.Keys(m))
	sort.Strings(out)
	return out
}

// Least is the contrast text needs on its background.
const Least = 4.5

// Contrast names the pairs of text and background that read under Least in a scheme with some accent.
func (t *Theme) Contrast() []string {
	var out []string
	for _, scheme := range t.Schemes {
		toks := t.tokens(scheme)
		worst, best := map[string]float64{}, map[string]float64{}
		with := map[string]string{}
		for _, accent := range Palette.Accents {
			cs := &colors{tokens: toks, accent: accent}
			pairs, err := cs.pairs(scheme)
			if err != nil {
				return append(out, fmt.Sprintf("%s: %v", scheme, err))
			}
			for k, r := range pairs {
				if w, ok := worst[k]; !ok || r < w {
					worst[k], with[k] = r, accent
				}
				best[k] = max(best[k], r)
			}
		}
		for _, k := range sorted(worst) {
			switch {
			case worst[k] >= Least:
			case best[k]-worst[k] > 0.05:
				out = append(out, fmt.Sprintf("%s: %s reads %.1f:1 with the accent %s; text needs %.1f:1", scheme, k, worst[k], with[k], Least))
			default:
				out = append(out, fmt.Sprintf("%s: %s reads %.1f:1; text needs %.1f:1", scheme, k, worst[k], Least))
			}
		}
	}
	return out
}

// pairs is the contrast of text and dimmed on each surface they sit on, of the text of a card on the cockpit and a
// card without a cover under its shade, and of a toast.
func (cs *colors) pairs(scheme string) (map[string]float64, error) {
	base := rgba{1, 1, 1, 1}
	if scheme == "dark" {
		base = rgba{0, 0, 0, 1}
	}
	get := func(n string) ([]rgba, error) {
		v, err := cs.stops(cs.tokens[n])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		return v, nil
	}
	canvas, err := get("canvas")
	if err != nil {
		return nil, err
	}
	surfaces := map[string][]rgba{}
	for i := range canvas {
		canvas[i] = canvas[i].over(base)
	}
	surfaces["canvas"] = canvas
	for _, n := range []string{"body", "island", "block"} {
		cs, err := get(n)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			for _, under := range canvas {
				surfaces[n] = append(surfaces[n], c.over(under))
			}
		}
	}
	out := map[string]float64{}
	least := func(k string, fg rgba, bgs []rgba) {
		for _, bg := range bgs {
			if r := contrast(fg, bg); out[k] == 0 || r < out[k] {
				out[k] = r
			}
		}
	}
	shade := []rgba{{}}
	if cs.tokens["tile-shade"] != "none" {
		if shade, err = get("tile-shade"); err != nil {
			return nil, err
		}
	}
	for _, pair := range [][3]string{{"tile-text", "cockpit", "tile-shade"}, {"tile-text", "tile", "tile-shade"}, {"toast-text", "toast", ""}} {
		if cs.tokens[pair[1]] == "" {
			continue
		}
		fg, err := cs.color(cs.tokens[pair[0]])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pair[0], err)
		}
		bgs, err := get(pair[1])
		if err != nil {
			return nil, err
		}
		for i := range bgs {
			bgs[i] = bgs[i].over(base)
			if pair[2] != "" {
				bgs[i] = shade[len(shade)-1].over(bgs[i])
			}
		}
		least(pair[0]+" on "+pair[1], fg, bgs)
	}
	// A veil lies over a picture of any color: the text on it is read over white and over black under it.
	if v := cs.tokens["tile-veil"]; v != "" {
		fg, err := cs.color(cs.tokens["tile-text"])
		if err != nil {
			return nil, fmt.Errorf("tile-text: %w", err)
		}
		veil, err := cs.color(v)
		if err != nil {
			return nil, fmt.Errorf("tile-veil: %w", err)
		}
		least("tile-text on tile-veil", fg, []rgba{veil.over(rgba{1, 1, 1, 1}), veil.over(rgba{0, 0, 0, 1})})
	}
	for _, fg := range []string{"text", "dimmed"} {
		c, err := cs.color(cs.tokens[fg])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fg, err)
		}
		for n, bgs := range surfaces {
			if fg == "dimmed" && n == "canvas" {
				continue
			}
			least(fg+" on "+n, c, bgs)
		}
	}
	return out, nil
}

// ContrastOver names the pairs that read under Least when the token of a card is the main color of its picture, by
// scheme; a scheme without a color is left as it is.
func (t *Theme) ContrastOver(token string, colors map[string]string) []string {
	over := *t
	over.Light, over.Dark = maps.Clone(t.Light), maps.Clone(t.Dark)
	if over.Light == nil {
		over.Light = map[string]string{}
	}
	if over.Dark == nil {
		over.Dark = map[string]string{}
	}
	for scheme, c := range colors {
		if c == "" {
			continue
		}
		if scheme == "dark" {
			over.Dark[token] = c
		} else {
			over.Light[token] = c
		}
	}
	var out []string
	for _, p := range over.Contrast() {
		if strings.Contains(p, "on "+token) {
			out = append(out, "over its picture: "+p)
		}
	}
	return out
}

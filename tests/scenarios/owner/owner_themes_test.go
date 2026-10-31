package owner

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// A theme has its pictures apart from its text: the scene of Today and the backdrop by scheme, which the client gets
// with the theme; the words of Today are checked over the scene, and a deleted theme takes its pictures along.
func TestAThemeHasItsPictures(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Must("deck.theme_save", map[string]any{"name": "paper", "text": `{"version": 1, "title": {"en": "Paper"}, "mood": "m", "tokens": {"tile-shade": "none", "cockpit": "#1b1b21"}}`}, nil)
	put := func(slot, scheme string, c color.RGBA) {
		id := e.Upload("master", slot+".png", "image/png", harness.Solid(c))
		e.Must("deck.theme_image", map[string]any{"theme": "paper", "slot": slot, "scheme": scheme, "file": fmt.Sprint(id)}, nil)
	}
	put("cockpit", "light", color.RGBA{250, 250, 245, 255})
	put("backdrop", "dark", color.RGBA{20, 22, 30, 255})
	var l themeLook
	e.Must("deck.theme", map[string]any{"name": "paper"}, &l)
	scene, back := l.Images["cockpit"], l.Images["backdrop"]
	if !strings.HasPrefix(scene.Light, "/api/files/") || scene.Dark != scene.Light || !strings.HasPrefix(scene.Color, "#f") || back.Light != back.Dark || back.Light == "" {
		t.Fatalf("the pictures of the theme by slot, a scheme without its own taking the other: %+v", l.Images)
	}
	if code, _ := e.Do("GET", back.Light, nil); code != 200 {
		t.Fatalf("the client reads the backdrop: %d", code)
	}
	var c struct {
		OK       bool
		Problems []string
	}
	e.Must("deck.theme_check", map[string]any{"name": "paper"}, &c)
	if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), "over its picture: light: tile-text on cockpit") {
		t.Fatalf("white words of Today do not read over a white scene: %+v", c)
	}
	e.Refused("deck.theme_image", map[string]any{"theme": "nope", "slot": "cockpit", "remove": true}, 404, `no theme "nope"`)
	e.Must("deck.theme_image", map[string]any{"theme": "paper", "slot": "cockpit", "remove": true}, nil)
	l = themeLook{}
	e.Must("deck.theme", map[string]any{"name": "paper"}, &l)
	if _, ok := l.Images["cockpit"]; ok {
		t.Fatalf("a picture taken off is gone: %+v", l.Images)
	}
	e.Must("deck.theme_delete", map[string]any{"name": "paper"}, nil)
	var left int
	// The pictures of a deleted theme, which no read shows.
	_ = e.SQLRow(`SELECT count(*) FROM picture WHERE theme = 'paper'`).Scan(&left)
	if left != 0 {
		t.Fatalf("a deleted theme takes its pictures along: %d left", left)
	}
}

// The owner asks Deki to draw the pictures of the theme in use; the master is told what and how, in the owner's words.
func TestTheMasterIsAskedForThePicturesOfTheTheme(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	var out struct{ Topic int64 }
	e.Must("deck.theme_draw", map[string]any{"wish": "облака потеплее"}, &out)
	msgs := e.ChatMessages(out.Topic)
	if len(msgs) == 0 || !strings.Contains(msgs[0].Text, "Нарисуй картинки темы Juce") || !strings.Contains(msgs[0].Text, "deck.theme_image") ||
		!strings.Contains(msgs[0].Text, "облака потеплее") {
		t.Fatalf("the master is told to draw the pictures and set them: %+v", msgs)
	}
}

// A workspace whose theme deck no longer ships wears juce and is told so once.
func TestAGoneThemeGivesWayToJuceAndSaysSo(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	// The theme glass an older deck shipped and the owner wore.
	e.SQL(`INSERT INTO setting (workspace_id, key, value) VALUES (1, 'theme', 'glass')
		ON CONFLICT (workspace_id, key) DO UPDATE SET value = excluded.value`)
	e.App.Themes.Settle(context.Background())
	e.App.Themes.Settle(context.Background())
	var l themeLook
	e.Must("deck.theme", map[string]any{}, &l)
	said := 0
	for _, n := range e.Notes() {
		if strings.Contains(n.Text, "glass") && strings.Contains(n.Text, "juce") {
			said++
		}
	}
	if l.Name != "juce" || said != 1 {
		t.Fatalf("juce is worn and the owner told once: %s, %d notes", l.Name, said)
	}
}

type themeLook struct {
	Name, Title, Mood, Accent, Radius, Density, CSS string
	Schemes                                         []string
	Problems                                        []string
	Current                                         bool
	Images                                          map[string]struct{ Light, Dark, Color string }
}

func themes(e *harness.Env) map[string]harness.ThemeRow {
	e.T.Helper()
	var rows []harness.ThemeRow
	e.Must("deck.themes_list", map[string]any{"lang": "ru"}, &rows)
	out := map[string]harness.ThemeRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

const nightTheme = `{"version": 1, "title": {"en": "Night", "ru": "Ночь"}, "mood": "Deep blue night, stars, soft glow.",
  "schemes": ["dark"], "radius": "lg",
  "dark": {"canvas": "#05070d", "body": "#0d1220", "text": "#e8ecf8", "dimmed": "#a3acc4"},
  "parts": {"button": {"transform": "uppercase", "radius": "xl"}, "island": {"radius": "18px"}},
  "css": ".nav .asks { color: var(--accent-3); }\n.dark .tab[data-active]:hover { color: var(--text); }"}`

// TestThemesAreCheckedDocuments: shipped themes pass the checks, a refused one is not saved, the owner's is a document
// over a shipped one worn by the whole deck, and a stored one a later check refuses is worn without what it refuses.
func TestThemesAreCheckedDocuments(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))

	all := themes(e)
	if r := all["juce"]; len(all) != 1 || !r.Builtin || r.Own || len(r.Problems) > 0 || !r.Current {
		t.Fatalf("deck ships juce alone and wears it first: %+v", all)
	}
	for _, n := range []string{"default", "glass", "pixel", "neon", "paper"} {
		e.Example(n)
	}
	all = themes(e)
	for _, n := range []string{"default", "glass", "pixel", "neon", "paper"} {
		if r := all[n]; !r.Own || r.Builtin || len(r.Problems) > 0 {
			t.Fatalf("the example %s of the skill is a theme of the owner that passes the checks: %+v", n, r)
		}
	}
	if all["paper"].Title != "Бумага" {
		t.Fatalf("titles translate: %+v", all["paper"])
	}
	var l themeLook
	e.Must("deck.theme", map[string]any{}, &l)
	clouds := l.Images["backdrop"]
	if code, body := e.Do("GET", clouds.Light, nil); code != 200 || len(body) < 1000 || clouds.Dark != "/api/files/theme/juce/backdrop-dark.webp" {
		t.Fatalf("the clouds of juce are its pictures, shipped with deck: %+v %d", l.Images, code)
	}
	if l.Name != "juce" || l.Accent != "coral" || !strings.Contains(l.CSS, "--deck-canvas: linear-gradient(180deg, #cfe6fa") ||
		!strings.Contains(l.CSS, "--deck-island-radius: 24px") || !strings.Contains(l.CSS, "[data-phone-tabs] [data-asks]") || l.Mood == "" {
		t.Fatalf("juce as the client wears it: %+v", l)
	}

	bad := map[string]string{
		"does not match its schema":     `{"version": 1, "title": {"en": "X"}, "mood": "m", "colour": "red"}`,
		"dimmed on island reads":        `{"version": 1, "title": {"en": "X"}, "mood": "m", "light": {"dimmed": "#cccccc"}}`,
		"text on block reads":           `{"version": 1, "title": {"en": "X"}, "mood": "m", "dark": {"block": "#ffffff"}}`,
		"no url() in a theme":           `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": ".button { background: url(https://x.example/a.png) }"}`,
		"no @import in a theme":         `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": "@import url(x.css); .nav { color: red }"}`,
		"no class .evil in the catalog": `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": ".evil { display: none }"}`,
		"has no place in a selector":    `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": "body { display: none }"}`,
		"of letters, digits, - and _":   `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": ".button[data-variant=\"x']{} body{display:none} .tab[data-active='\"] { color: red }"}`,
		"url ( is no function":          `{"version": 1, "title": {"en": "X"}, "mood": "m", "tokens": {"sky": "url/**/(https://x.example/a.png)"}}`,
		"--text of the page is a token": `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": ".nav, .root { --text: #ffffff }"}`,
		"no variable --mantine-x":       `{"version": 1, "title": {"en": "X"}, "mood": "m", "tokens": {"fill": "var(--mantine-x)"}}`,
	}
	for want, text := range bad {
		var c struct {
			OK       bool
			Problems []string
		}
		e.Must("deck.theme_check", map[string]any{"text": text}, &c)
		if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), want) {
			t.Fatalf("check of %s: want %q, got %+v", text, want, c)
		}
		e.Refused("deck.theme_save", map[string]any{"name": "bad", "text": text}, 422, want)
	}
	e.Refused("deck.document_save", map[string]any{"name": "themes/x.json", "kind": "theme", "text": nightTheme}, 422, "deck.theme_save")

	var saved struct {
		Revision int64
		Preview  []string
	}
	e.Must("deck.theme_save", map[string]any{"name": "night", "text": nightTheme, "reason": "a dark one"}, &saved)
	if saved.Revision != 1 || len(saved.Preview) != 2 || saved.Preview[1] != "/?theme=night&scheme=dark" {
		t.Fatalf("saved: %+v", saved)
	}
	e.Must("deck.theme_use", map[string]any{"name": "night"}, nil)
	e.Must("deck.theme", map[string]any{"lang": "ru"}, &l)
	if l.Name != "night" || !l.Current || l.Title != "Ночь" || len(l.Schemes) != 1 || l.Radius != "lg" ||
		!strings.Contains(l.CSS, "text-transform: uppercase") || !strings.Contains(l.CSS, "border-radius: var(--mantine-radius-xl)") ||
		!strings.Contains(l.CSS, "[data-mantine-color-scheme='dark'] .mantine-Tabs-tab[data-active]:hover") ||
		strings.Contains(l.CSS, "data-mantine-color-scheme='light'] {") {
		t.Fatalf("night as the client wears it: %+v", l)
	}

	juce := strings.Replace(nightTheme, `"Night"`, `"My juce"`, 1)
	e.Must("deck.theme_save", map[string]any{"name": "juce", "text": juce}, nil)
	if r := themes(e)["juce"]; !r.Own || !r.Builtin || r.Title != "Ночь" {
		t.Fatalf("the owner's juce over the shipped one: %+v", r)
	}
	e.Must("deck.theme_delete", map[string]any{"name": "juce"}, nil)
	if r := themes(e)["juce"]; r.Own || r.Title != "Juce" {
		t.Fatalf("the shipped juce is back: %+v", r)
	}
	e.Refused("deck.theme_delete", map[string]any{"name": "juce"}, 422, "deck ships the theme juce")

	// A theme stored before a check of today: worn, without what the check refuses.
	old := strings.Replace(nightTheme, `.nav .asks { color: var(--accent-3); }`, `.nav .asks { color: var(--accent-3); background: url(x.png); } .gone { color: red }`, 1)
	// A theme a deck before today's check stored.
	e.PlantRevision("", "themes/night.json", old)
	e.Must("deck.theme", map[string]any{}, &l)
	if l.Name != "night" || len(l.Problems) != 2 || strings.Contains(l.CSS, "url(") || !strings.Contains(l.CSS, "[data-phone-tabs] [data-asks]") {
		t.Fatalf("an old theme is worn without what a check refuses: %+v", l)
	}
	var c struct{ OK bool }
	e.Must("deck.theme_check", map[string]any{"name": "night"}, &c)
	if c.OK {
		t.Fatal("the check names the problems of a stored theme")
	}

	e.Must("deck.theme_delete", map[string]any{"name": "night"}, nil)
	e.Must("deck.theme", map[string]any{}, &l)
	if l.Name != "juce" || !l.Current {
		t.Fatalf("a deleted theme in use gives way to juce: %+v", l)
	}
}

// TestThemesStyleTheCardsOfModules: the cockpit's card takes the accent until a theme gives its own, a theme styles the
// cards of modules, their grid, the toast and the parts of deck by the catalog, and faint text on a card is refused.
func TestThemesStyleTheCardsOfModules(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	var l themeLook
	e.Must("deck.theme", map[string]any{}, &l)
	if !strings.Contains(l.CSS, "--deck-cockpit: linear-gradient(135deg, var(--mantine-primary-color-5), var(--mantine-primary-color-8))") ||
		strings.Contains(l.CSS, "--deck-tile:") || !strings.Contains(l.CSS, "--deck-tile-gap: var(--mantine-spacing-xl)") || len(l.Problems) > 0 {
		t.Fatalf("the cards of juce: the cockpit's in the accent, the others by their names: %+v", l)
	}

	cards := `{"version": 1, "title": {"en": "Cards"}, "mood": "m", "schemes": ["light"],
  "tokens": {"cockpit": "linear-gradient(180deg, #ffffff, #f2f6ee)", "tile": "#ffffff", "tile-text": "#0e1116", "tile-shade": "none",
    "tile-icon": "var(--accent-light)", "tile-min": "240px", "tile-height": "200px", "tile-lift": "translateY(-2px)",
    "meadow": "radial-gradient(70% 100% at 22% 100%, #b7d79a 0%, transparent 75%)", "toast": "#1b1b21"},
  "parts": {"tile": {"radius": "24px", "shadow": "var(--ring), var(--shadow)"}, "notification": {"radius": "14px"}, "tabs": {"weight": "600"},
    "bubble": {"radius": "18px"}, "menu": {"radius": "12px"}, "dialog": {"radius": "20px"}},
  "css": ".today[data-cover] .h2 { color: var(--tile-text) }\n.tile:not([data-big]) .tile-icon { color: var(--accent) }\n.board-card { border-radius: 8px }\n.toast .button { font-weight: 600 }\n.meadow { opacity: 0.8 }\n.place[data-active] { font-weight: 700 }"}`
	var c struct {
		OK       bool
		Problems []string
	}
	e.Must("deck.theme_check", map[string]any{"text": cards}, &c)
	if !c.OK {
		t.Fatalf("a theme of white cards with dark text passes: %+v", c)
	}
	e.Must("deck.theme_save", map[string]any{"name": "cards", "text": cards}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "cards"}, nil)
	e.Must("deck.theme", map[string]any{}, &l)
	for _, want := range []string{"--deck-tile: #ffffff", "--deck-cockpit: linear-gradient(180deg, #ffffff, #f2f6ee)", "--deck-tile-min: 240px",
		"--deck-meadow: radial-gradient", "[data-app-tile]:not([data-big]) { border-radius: 24px; box-shadow: var(--deck-ring), var(--deck-shadow); }",
		":is(.mantine-Notification-root, [data-toast]) { border-radius: 14px; }", ":is(.mantine-Tabs-tab, [data-places] > a) { font-weight: 600; }",
		"[data-app-tile][data-big][data-cover] h2", "[data-app-tile]:not([data-big]) [data-app-tile-icon]", "[data-places] > a[data-active]", "[data-lane] [data-card]", "[data-toast] .mantine-Button-root",
		"[data-meadow]"} {
		if !strings.Contains(l.CSS, want) {
			t.Fatalf("the cards theme as the client wears it has %q: %s", want, l.CSS)
		}
	}

	faint := map[string]string{
		"tile-text on cockpit reads":   `{"version": 1, "title": {"en": "X"}, "mood": "m", "tokens": {"cockpit": "#fff3bf", "tile-shade": "none"}}`,
		"tile-text on tile reads":      `{"version": 1, "title": {"en": "X"}, "mood": "m", "tokens": {"tile": "#ffffff", "tile-shade": "none"}}`,
		"toast-text on toast reads":    `{"version": 1, "title": {"en": "X"}, "mood": "m", "light": {"toast": "#f1f3f5"}}`,
		"tile-text on tile-veil reads": `{"version": 1, "title": {"en": "X"}, "mood": "m", "tokens": {"tile-veil": "rgb(0 0 0 / 0.2)"}}`,
		"a theme reads the states":     `{"version": 1, "title": {"en": "X"}, "mood": "m", "css": ".tile[data-app] { color: red }"}`,
	}
	for want, text := range faint {
		e.Must("deck.theme_check", map[string]any{"text": text}, &c)
		if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), want) {
			t.Fatalf("check of %s: want %q, got %+v", text, want, c)
		}
	}
}

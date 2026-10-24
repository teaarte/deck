package scenarios

import (
	"context"
	"strings"
	"testing"

	"deck2/internal/docs"
	"deck2/internal/store/db"
)

type themeLook struct {
	Name, Title, Mood, Accent, Radius, Density, Backdrop, CSS string
	Schemes                                                   []string
	Problems                                                  []string
	Current                                                   bool
}

type themeRow struct {
	Name     string
	Title    string
	Current  bool
	Builtin  bool
	Own      bool
	Problems []string
}

func (e *Env) themes() map[string]themeRow {
	e.T.Helper()
	var rows []themeRow
	e.must("deck.themes_list", map[string]any{"lang": "ru"}, &rows)
	out := map[string]themeRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

const nightTheme = `{"version": 1, "title": {"en": "Night", "ru": "Ночь"}, "mood": "Deep blue night, stars, soft glow.",
  "schemes": ["dark"], "backdrop": "plain", "radius": "lg",
  "dark": {"canvas": "#05070d", "body": "#0d1220", "text": "#e8ecf8", "dimmed": "#a3acc4"},
  "parts": {"button": {"transform": "uppercase", "radius": "xl"}, "island": {"radius": "18px"}},
  "css": ".nav .asks { color: var(--accent-3); }\n.dark .tab[data-active]:hover { color: var(--text); }"}`

// TestThemesAreCheckedDocuments: shipped themes pass the checks, a refused one is not saved, the owner's is a document
// over a shipped one worn by the whole deck, and a stored one a later check refuses is worn without what it refuses.
func TestThemesAreCheckedDocuments(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)

	all := e.themes()
	for _, n := range []string{"juce", "default", "glass", "pixel", "neon", "paper"} {
		r, ok := all[n]
		if !ok || !r.Builtin || r.Own || len(r.Problems) > 0 {
			t.Fatalf("shipped theme %s: %+v", n, r)
		}
	}
	if !all["juce"].Current || all["paper"].Title != "Бумага" {
		t.Fatalf("juce is worn first, titles translate: %+v", all)
	}
	var l themeLook
	e.must("deck.theme", map[string]any{}, &l)
	if l.Name != "juce" || l.Accent != "coral" || l.Backdrop != "sky" || !strings.Contains(l.CSS, "--deck-canvas: linear-gradient(180deg, #cfe6fa") ||
		!strings.Contains(l.CSS, "--deck-island-radius: 24px") || !strings.Contains(l.CSS, "[data-nav] [data-asks]") || l.Mood == "" {
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
		e.must("deck.theme_check", map[string]any{"text": text}, &c)
		if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), want) {
			t.Fatalf("check of %s: want %q, got %+v", text, want, c)
		}
		e.refused("deck.theme_save", map[string]any{"name": "bad", "text": text}, 422, want)
	}
	e.refused("deck.document_save", map[string]any{"name": "themes/x.json", "kind": "theme", "text": nightTheme}, 422, "deck.theme_save")

	var saved struct {
		Revision int64
		Preview  []string
	}
	e.must("deck.theme_save", map[string]any{"name": "night", "text": nightTheme, "reason": "a dark one"}, &saved)
	if saved.Revision != 1 || len(saved.Preview) != 2 || saved.Preview[1] != "/?theme=night&scheme=dark" {
		t.Fatalf("saved: %+v", saved)
	}
	e.must("deck.theme_use", map[string]any{"name": "night"}, nil)
	e.must("deck.theme", map[string]any{"lang": "ru"}, &l)
	if l.Name != "night" || !l.Current || l.Title != "Ночь" || len(l.Schemes) != 1 || l.Radius != "lg" ||
		!strings.Contains(l.CSS, "text-transform: uppercase") || !strings.Contains(l.CSS, "border-radius: var(--mantine-radius-xl)") ||
		!strings.Contains(l.CSS, "[data-mantine-color-scheme='dark'] .mantine-Tabs-tab[data-active]:hover") ||
		strings.Contains(l.CSS, "data-mantine-color-scheme='light'] {") {
		t.Fatalf("night as the client wears it: %+v", l)
	}

	paper := strings.Replace(nightTheme, `"Night"`, `"My paper"`, 1)
	e.must("deck.theme_save", map[string]any{"name": "paper", "text": paper}, nil)
	if r := e.themes()["paper"]; !r.Own || !r.Builtin || r.Title != "Ночь" {
		t.Fatalf("the owner's paper over the shipped one: %+v", r)
	}
	e.must("deck.theme_delete", map[string]any{"name": "paper"}, nil)
	if r := e.themes()["paper"]; r.Own || r.Title != "Бумага" {
		t.Fatalf("the shipped paper is back: %+v", r)
	}
	e.refused("deck.theme_delete", map[string]any{"name": "juce"}, 422, "deck ships the theme juce")

	// A theme stored before a check of today: worn, without what the check refuses.
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		old := strings.Replace(nightTheme, `.nav .asks { color: var(--accent-3); }`, `.nav .asks { color: var(--accent-3); background: url(x.png); } .gone { color: red }`, 1)
		_, _, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.Scope{Workspace: 1}, Name: "themes/night.json"}, docs.KindTheme, old, "owner", "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.must("deck.theme", map[string]any{}, &l)
	if l.Name != "night" || len(l.Problems) != 2 || strings.Contains(l.CSS, "url(") || !strings.Contains(l.CSS, "[data-nav] [data-asks]") {
		t.Fatalf("an old theme is worn without what a check refuses: %+v", l)
	}
	var c struct{ OK bool }
	e.must("deck.theme_check", map[string]any{"name": "night"}, &c)
	if c.OK {
		t.Fatal("the check names the problems of a stored theme")
	}

	e.must("deck.theme_delete", map[string]any{"name": "night"}, nil)
	e.must("deck.theme", map[string]any{}, &l)
	if l.Name != "juce" || !l.Current {
		t.Fatalf("a deleted theme in use gives way to juce: %+v", l)
	}
}

package scenarios

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"

	"deck2/internal/store/db"
)

func solid(c color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.String()
}

// A theme has its pictures apart from its text: the scene of Today and the backdrop by scheme, which the client gets
// with the theme; the words of Today are checked over the scene, and a deleted theme takes its pictures along.
func TestAThemeHasItsPictures(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	e.must("deck.theme_save", map[string]any{"name": "paper", "text": `{"version": 1, "title": {"en": "Paper"}, "mood": "m", "tokens": {"tile-shade": "none", "cockpit": "#1b1b21"}}`}, nil)
	put := func(slot, scheme string, c color.RGBA) {
		id := e.upload("master", slot+".png", "image/png", solid(c))
		e.must("deck.theme_image", map[string]any{"theme": "paper", "slot": slot, "scheme": scheme, "file": fmt.Sprint(id)}, nil)
	}
	put("cockpit", "light", color.RGBA{250, 250, 245, 255})
	put("backdrop", "dark", color.RGBA{20, 22, 30, 255})
	var l themeLook
	e.must("deck.theme", map[string]any{"name": "paper"}, &l)
	scene, back := l.Images["cockpit"], l.Images["backdrop"]
	if !strings.HasPrefix(scene.Light, "/api/files/") || scene.Dark != scene.Light || !strings.HasPrefix(scene.Color, "#f") || back.Light != back.Dark || back.Light == "" {
		t.Fatalf("the pictures of the theme by slot, a scheme without its own taking the other: %+v", l.Images)
	}
	if code, _ := e.do("GET", back.Light, nil); code != 200 {
		t.Fatalf("the client reads the backdrop: %d", code)
	}
	var c struct {
		OK       bool
		Problems []string
	}
	e.must("deck.theme_check", map[string]any{"name": "paper"}, &c)
	if c.OK || !strings.Contains(strings.Join(c.Problems, "\n"), "over its picture: light: tile-text on cockpit") {
		t.Fatalf("white words of Today do not read over a white scene: %+v", c)
	}
	e.refused("deck.theme_image", map[string]any{"theme": "nope", "slot": "cockpit", "remove": true}, 404, `no theme "nope"`)
	e.must("deck.theme_image", map[string]any{"theme": "paper", "slot": "cockpit", "remove": true}, nil)
	l = themeLook{}
	e.must("deck.theme", map[string]any{"name": "paper"}, &l)
	if _, ok := l.Images["cockpit"]; ok {
		t.Fatalf("a picture taken off is gone: %+v", l.Images)
	}
	e.must("deck.theme_delete", map[string]any{"name": "paper"}, nil)
	var left int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM picture WHERE theme = 'paper'`).Scan(&left)
	if left != 0 {
		t.Fatalf("a deleted theme takes its pictures along: %d left", left)
	}
}

// The owner asks Deki to draw the pictures of the theme in use; the master is told what and how, in the owner's words.
func TestTheMasterIsAskedForThePicturesOfTheTheme(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	var out struct{ Topic int64 }
	e.must("deck.theme_draw", map[string]any{"wish": "облака потеплее"}, &out)
	msgs := e.chatMessages(out.Topic)
	if len(msgs) == 0 || !strings.Contains(msgs[0].Text, "Нарисуй картинки темы Juce") || !strings.Contains(msgs[0].Text, "deck.theme_image") ||
		!strings.Contains(msgs[0].Text, "облака потеплее") {
		t.Fatalf("the master is told to draw the pictures and set them: %+v", msgs)
	}
}

// A workspace whose theme deck no longer ships wears juce and is told so once.
func TestAGoneThemeGivesWayToJuceAndSaysSo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: 1, Key: "theme", Value: "glass"})
	})
	if err != nil {
		t.Fatal(err)
	}
	e.App.Themes.Settle(context.Background())
	e.App.Themes.Settle(context.Background())
	var l themeLook
	e.must("deck.theme", map[string]any{}, &l)
	said := 0
	for _, n := range e.notes() {
		if strings.Contains(n.Text, "glass") && strings.Contains(n.Text, "juce") {
			said++
		}
	}
	if l.Name != "juce" || said != 1 {
		t.Fatalf("juce is worn and the owner told once: %s, %d notes", l.Name, said)
	}
}

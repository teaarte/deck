package themes

import (
	"context"
	"deck2/internal/words"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/theme"
)

type drawPicturesIn struct {
	Wish  string `type:"text" opt:"" desc:"What the owner wishes to see in the pictures"`
	Model string `opt:"" desc:"The image model; a free one of deck without it"`
}

// drawPictures tells the master to draw the pictures of the theme in use: the scene of Today and what is behind the
// page; deck draws nothing itself.
func (s *Service) drawPictures(ctx context.Context, act registry.Actor, in drawPicturesIn) (any, error) {
	lang := s.Language(ctx, act.Workspace)
	ws := words.In(lang)
	cur := s.Current(ctx, act.Workspace)
	l, err := s.look(ctx, act.Workspace, cur, lang)
	if err != nil {
		return nil, err
	}
	model := in.Model
	if model == "" {
		model = s.freeImageModel(ctx, act)
	}
	if model == "" {
		model = "the image model deck gives"
	}
	b := []string{
		manifest.TrText(ws, fmt.Sprintf("Draw the pictures of the theme %s of deck: %s", l.Title, l.Mood)),
		manifest.TrText(ws, "The cockpit: the scene of the card of Today, wide, calm on the left under its words, with room for Deki on the right. Behind the page: a backdrop, one wide picture, or a pattern, a seamless square tile, as fits the mood; quiet, for it stays behind everything."),
	}
	if w := strings.TrimSpace(in.Wish); w != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("The owner wishes: %s", w)))
	}
	if len(l.Schemes) == 1 {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Pictures for the %s scheme, the only one of the theme: call deck.model_call of kind image on %s for each, then set each with deck.theme_image, theme %s, its slot, its file as results/<the name the call gave>, scheme %s.", l.Schemes[0], model, cur, l.Schemes[0])))
	} else {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Pictures for the light scheme and for the dark: call deck.model_call of kind image on %s for each, then set each with deck.theme_image, theme %s, its slot, its file as results/<the name the call gave>, scheme light or dark.", model, cur)))
	}
	b = append(b, manifest.TrText(ws, "Then check the words of Today over the cockpit with deck.theme_check and look at the tiles with deck.screen_shot."))
	input, _ := json.Marshal(map[string]any{"text": strings.Join(b, "\n\n")})
	return s.Invoke(ctx, act, registry.Builtin+".chat_send", input)
}

// Settle puts the default theme on a workspace whose theme is gone, such as one deck no longer ships, and says so
// once in the feed.
func (s *Service) Settle(ctx context.Context) {
	wss, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return
	}
	for _, ws := range wss {
		name, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: ws, Key: key})
		if err != nil || name == "" || s.Exists(ctx, ws, name) {
			continue
		}
		m, err := access.Master(ctx, s.St.Q, ws)
		if err != nil {
			continue
		}
		err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: ws, Key: key, Value: theme.Default}); err != nil {
				return err
			}
			return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(s.Now()), ProjectID: m.ID,
				Text: fmt.Sprintf("The theme %s is gone from deck, so deck wears %s now; ask Deki to make %s again from its example.", name, theme.Default, name)})
		})
		if err != nil {
			slog.Error("themes: settle the theme of a workspace", "workspace", ws, "err", err)
		}
	}
}

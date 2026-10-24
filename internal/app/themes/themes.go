// Package themes keeps the themes of deck: the shipped ones, the owner's documents over them, and the one in use.
package themes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/theme"
	"deck2/schemas"
)

type Service struct {
	St  *store.Store
	Now func() time.Time
	// Invoke calls a command of deck: the master is told through deck.chat_send.
	Invoke func(ctx context.Context, act registry.Actor, name string, input json.RawMessage) (any, error)
	// Words are deck's words in a language, Language the one the workspace picked.
	Words    func(lang string) map[string]string
	Language func(ctx context.Context, workspace int64) string
}

// Format is the schema of a theme.
func Format() json.RawMessage { return schemas.Theme }

// key is the setting of the theme a workspace wears.
const key = "theme"

func scope(act registry.Actor) docs.Scope { return docs.Scope{Workspace: act.Workspace} }

// text is the text of a theme: the owner's document, or the shipped one; own says it is a document.
func (s *Service) text(ctx context.Context, q *db.Queries, ws int64, name string) (text []byte, own bool, err error) {
	t, err := docs.CurrentText(ctx, q, docs.Scope{Workspace: ws}, docs.KindTheme, theme.DocName(name))
	if err != nil {
		return nil, false, err
	}
	if t != "" {
		return []byte(t), true, nil
	}
	return theme.Builtin(name), false, nil
}

// Current is the name of the theme a workspace wears; a deleted one gives way to the default.
func (s *Service) Current(ctx context.Context, ws int64) string {
	name, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: ws, Key: key})
	if err != nil || name == "" {
		return theme.Default
	}
	if t, _, err := s.text(ctx, s.St.Q, ws, name); err != nil || t == nil {
		return theme.Default
	}
	return name
}

// Mood is the style of pictures of the theme a workspace wears.
func (s *Service) Mood(ctx context.Context, ws int64) string {
	l, err := s.look(ctx, ws, s.Current(ctx, ws), "")
	if err != nil {
		return ""
	}
	return l.Mood
}

func (s *Service) look(ctx context.Context, ws int64, name, lang string) (theme.Look, error) {
	raw, _, err := s.text(ctx, s.St.Q, ws, name)
	if err != nil {
		return theme.Look{}, err
	}
	if raw == nil {
		return theme.Look{}, registry.NotFound("deck: no theme %q; deck.themes_list names them", name)
	}
	t, err := theme.Parse(raw)
	if err != nil {
		return theme.Look{}, err
	}
	return t.Wear(name, lang), nil
}

type themeIn struct {
	Name string `opt:"" desc:"The theme; the one in use without it"`
	Lang string `opt:"" desc:"Language of the title, such as ru"`
}

type lookOut struct {
	theme.Look
	Current bool `json:"current"`
	// Text is the JSON of the theme to start a change from.
	Text string `json:"text"`
}

func (s *Service) theme(ctx context.Context, act registry.Actor, in themeIn) (lookOut, error) {
	cur := s.Current(ctx, act.Workspace)
	name := in.Name
	if name == "" {
		name = cur
	}
	l, err := s.look(ctx, act.Workspace, name, in.Lang)
	if err != nil && in.Name == "" && name != theme.Default {
		name = theme.Default
		l, err = s.look(ctx, act.Workspace, name, in.Lang)
	}
	raw, _, _ := s.text(ctx, s.St.Q, act.Workspace, name)
	return lookOut{Look: l, Current: name == cur, Text: string(raw)}, err
}

type themeRow struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Mood    string `json:"mood"`
	Current bool   `json:"current"`
	// Builtin is a theme deck ships; Own is the owner's document, over the shipped one of its name when there is one.
	Builtin  bool     `json:"builtin"`
	Own      bool     `json:"own"`
	Schemes  []string `json:"schemes"`
	Problems []string `json:"problems,omitempty"`
}

type listIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru"`
}

func (s *Service) list(ctx context.Context, act registry.Actor, in listIn) ([]themeRow, error) {
	ds, err := s.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: act.Workspace, Kind: docs.KindTheme})
	if err != nil {
		return nil, err
	}
	names := theme.Builtins()
	for _, d := range ds {
		if n := theme.NameOf(d.Name); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	cur := s.Current(ctx, act.Workspace)
	var out []themeRow
	for _, n := range names {
		raw, own, err := s.text(ctx, s.St.Q, act.Workspace, n)
		if err != nil {
			return nil, err
		}
		row := themeRow{Name: n, Title: n, Current: n == cur, Builtin: theme.Builtin(n) != nil, Own: own}
		if t, err := theme.Parse(raw); err != nil {
			row.Problems = []string{err.Error()}
		} else {
			l := t.Wear(n, in.Lang)
			row.Title, row.Mood, row.Schemes, row.Problems = l.Title, l.Mood, l.Schemes, l.Problems
		}
		out = append(out, row)
	}
	return out, nil
}

type checkIn struct {
	Name string `opt:"" desc:"A saved theme to check"`
	Text string `type:"text" opt:"" desc:"The JSON text of a theme to check without saving it"`
}

type checkOut struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
	// Preview are the addresses of deck in the theme, the light and the dark scheme, for a saved theme.
	Preview []string `json:"preview,omitempty"`
}

func preview(name string) []string {
	return []string{"/?theme=" + name + "&scheme=light", "/?theme=" + name + "&scheme=dark"}
}

// check is what a check of today refuses in a theme.
func check(name string, raw []byte) []string {
	t, err := theme.Parse(raw)
	if err != nil {
		return []string{err.Error()}
	}
	return t.Wear(name, "").Problems
}

func (s *Service) check(ctx context.Context, act registry.Actor, in checkIn) (any, error) {
	raw := []byte(in.Text)
	if in.Text == "" {
		if in.Name == "" {
			return nil, registry.Invalid("deck: give a theme name or its text")
		}
		var err error
		if raw, _, err = s.text(ctx, s.St.Q, act.Workspace, in.Name); err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, registry.NotFound("deck: no theme %q", in.Name)
		}
	}
	out := checkOut{Problems: check(in.Name, raw)}
	out.OK = len(out.Problems) == 0
	if in.Text == "" {
		out.Preview = preview(in.Name)
	}
	if out.Problems == nil {
		out.Problems = []string{}
	}
	return out, nil
}

type saveIn struct {
	Name       string `desc:"The theme: lowercase letters, digits and -; a shipped theme's name puts the owner's over it"`
	Text       string `type:"text" desc:"The whole JSON text of the theme by deck.theme_format"`
	IfRevision int64  `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
	Reason     string `opt:"" desc:"Why it changed"`
}

func (s *Service) save(ctx context.Context, act registry.Actor, in saveIn) (any, error) {
	if err := theme.CheckName(in.Name); err != nil {
		return nil, registry.Invalid("deck: %v", err)
	}
	if ps := check(in.Name, []byte(in.Text)); len(ps) > 0 {
		return nil, registry.Invalid("deck: the theme is not saved: %s", strings.Join(ps, "; "))
	}
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.Ref{Scope: scope(act), Name: theme.DocName(in.Name)}
		if in.IfRevision != 0 {
			d, err := docs.Find(ctx, q, ref)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if d.Current != in.IfRevision {
				return registry.Conflict("deck: the theme is at revision %d now, not %d; read it again", d.Current, in.IfRevision)
			}
		}
		var err error
		_, n, _, err = docs.Save(ctx, q, ref, docs.KindTheme, in.Text, act.Author(), in.Reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name, "revision": n, "preview": preview(in.Name)}, nil
}

type nameIn struct {
	Name string `desc:"The theme"`
}

func (s *Service) remove(ctx context.Context, act registry.Actor, in nameIn) (any, error) {
	var builtin bool
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		d, err := docs.Find(ctx, q, docs.Ref{Scope: scope(act), Name: theme.DocName(in.Name)})
		builtin = theme.Builtin(in.Name) != nil
		switch {
		case errors.Is(err, sql.ErrNoRows) && builtin:
			return registry.Invalid("deck: deck ships the theme %s; it is not deleted", in.Name)
		case errors.Is(err, sql.ErrNoRows):
			return registry.NotFound("deck: no theme %q", in.Name)
		case err != nil:
			return err
		case d.Kind != docs.KindTheme:
			return registry.Invalid("deck: %s is no theme", d.Name)
		}
		_, err = q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(s.Now()), Valid: true}, ID: d.ID})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"name": in.Name, "deleted": true}
	if builtin {
		out["note"] = fmt.Sprintf("the shipped %s is back", in.Name)
	}
	return out, nil
}

func (s *Service) use(ctx context.Context, act registry.Actor, in nameIn) (any, error) {
	raw, _, err := s.text(ctx, s.St.Q, act.Workspace, in.Name)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, registry.NotFound("deck: no theme %q; deck.themes_list names them", in.Name)
	}
	if _, err := theme.Parse(raw); err != nil {
		return nil, registry.Invalid("deck: %v", err)
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: key, Value: in.Name})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name, "current": true}, nil
}

// Commands are the commands of themes.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("themes_list", "Themes",
			"Lists the themes of deck: the shipped ones and the owner's, which one is in use and what a check refuses in each.",
			registry.Everyone, s.list).On("documents", "settings"),
		registry.Read("theme", "Theme",
			"Returns a theme as the client applies it: its CSS, fonts, radius, density, backdrop and mood; the one in use without a name.",
			registry.Everyone, s.theme).On("documents", "settings"),
		registry.Def("theme_format", "Theme format",
			"Returns the JSON Schema of a theme with the catalog of its tokens, variables and classes.",
			registry.Everyone, func(context.Context, registry.Actor, struct{}) (any, error) { return Format(), nil }),
		registry.Def("theme_check", "Check theme",
			"Checks a theme without saving it: its schema, its CSS and the contrast of its text in both schemes with every accent; for a saved theme also the addresses of deck in it, light and dark.",
			registry.OwnerSide, s.check),
		registry.Def("theme_save", "Save theme",
			"Creates or changes a theme; one a check refuses is not saved. The name of a shipped theme puts this one over it.",
			registry.OwnerSide, s.save),
		registry.Def("theme_delete", "Delete theme",
			"Deletes the owner's theme; over a shipped one, the shipped one comes back.",
			registry.OwnerSide, s.remove),
		registry.Def("covers_draw", "Draw covers",
			"Asks the master to draw the covers of a module's card, light and dark, in the mood of the theme in use; without a module, every module that has a cover again.",
			registry.OwnerOnly, s.draw),
		registry.Def("theme_use", "Use theme",
			"Puts a theme on the whole deck.",
			registry.OwnerOnly, s.use),
	}
}

package files

import (
	"fmt"
	"slices"

	"deck2/internal/store/db"
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

// Cover is the picture of a project's card in the theme in use, by scheme: where the light copy is read and its main
// color; Theme is the theme it is for, empty for every theme.
type Cover struct {
	Light     string `json:"light,omitempty"`
	Dark      string `json:"dark,omitempty"`
	Color     string `json:"color,omitempty"`
	DarkColor string `json:"dark_color,omitempty"`
	Theme     string `json:"theme"`
}

// CoverOf is the cover of a project in a theme among the pictures of a workspace, nil without one.
func CoverOf(pics []db.Picture, project int64, theme string) *Cover {
	own := slices.DeleteFunc(slices.Clone(pics), func(x db.Picture) bool { return x.ProjectID != project })
	light, dark := Pick(own, theme, "light"), Pick(own, theme, "dark")
	if light == nil {
		return nil
	}
	c := &Cover{Light: fmt.Sprintf("/api/files/%d", Shown(*light)), Color: light.Color, Theme: light.Theme}
	if dark != nil {
		c.Dark, c.DarkColor = fmt.Sprintf("/api/files/%d", Shown(*dark)), dark.Color
	}
	return c
}

// ThemeImage is a picture of a theme in a slot by scheme, nil without one: of its own scheme, else of the other. A
// cockpit shows the light copy; a backdrop and a pattern fill the page, so they show the picture itself.
func ThemeImage(pics []db.Picture, slot, theme string) *Cover {
	own := slices.DeleteFunc(slices.Clone(pics), func(x db.Picture) bool { return x.Slot != slot || x.Theme != theme })
	light, dark := Pick(own, theme, "light"), Pick(own, theme, "dark")
	if light == nil {
		return nil
	}
	shown := func(p db.Picture) string {
		if slot == "cockpit" {
			return fmt.Sprintf("/api/files/%d", Shown(p))
		}
		return fmt.Sprintf("/api/files/%d", p.FileID)
	}
	return &Cover{Light: shown(*light), Dark: shown(*dark), Color: light.Color, DarkColor: dark.Color, Theme: theme}
}

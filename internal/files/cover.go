package files

import (
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

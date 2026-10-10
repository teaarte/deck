package manifest

import "cmp"

// Nav is how a project of name with manifest m shows in the menu and in the list of projects: its title, its icon
// and the screens of its menu, in the owner's words.
func Nav(name string, m *Manifest, ws map[string]string) (string, string, []ScreenLink) {
	names := m.Menu
	if len(names) == 0 {
		for _, s := range m.Screens {
			if s.Record == "" {
				names = append(names, s.Name)
			}
		}
	}
	links := []ScreenLink{}
	for _, n := range names {
		s := m.Screen(n)
		links = append(links, ScreenLink{Name: n, Title: Tr(ws, s.Title), Icon: s.Icon})
	}
	return Tr(ws, cmp.Or(m.Title, name)), m.Icon, links
}

// ScreenLink is a screen in the menu by its name, title and icon.
type ScreenLink struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// Tr is a text in a language by words, or the text itself.
func Tr(ws map[string]string, s string) string {
	if t := ws[s]; t != "" {
		return t
	}
	return s
}

package manifest

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
)

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
	if m.Title == "" {
		return name, m.Icon, links
	}
	return Tr(ws, m.Title), m.Icon, links
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

// FormPlace is where a form of a command opens in a screen: the tabs to it by key, outer first, its dialog when it
// is in one with a name, and the parameters of the screen its values read.
type FormPlace struct {
	Tabs   []string
	Dialog string
	Params []string
}

// FormOf is the place of the first form in s that calls command; false when s has none.
func FormOf(s *Screen, command string) (FormPlace, bool) {
	var find func(blocks []Block, at FormPlace) (FormPlace, bool)
	find = func(blocks []Block, at FormPlace) (FormPlace, bool) {
		for _, b := range blocks {
			switch {
			case b.Type == "form" && b.Command == command:
				for _, v := range b.Values {
					if expr, ok := strings.CutPrefix(v, "="); ok {
						for _, m := range paramRef.FindAllStringSubmatch(expr, -1) {
							if !slices.Contains(at.Params, m[1]) {
								at.Params = append(at.Params, m[1])
							}
						}
					}
				}
				slices.Sort(at.Params)
				return at, true
			case b.Type == "dialog" && b.Name != "":
				if f, ok := find(b.Blocks, FormPlace{Tabs: at.Tabs, Dialog: b.Name}); ok {
					return f, true
				}
			case b.Type == "tabs":
				for _, t := range b.Tabs {
					if f, ok := find(t.Blocks, FormPlace{Tabs: append(slices.Clip(at.Tabs), t.Title), Dialog: at.Dialog}); ok {
						return f, true
					}
				}
			default:
				if f, ok := find(b.Blocks, at); ok {
					return f, true
				}
			}
		}
		return FormPlace{}, false
	}
	return find(s.Blocks, FormPlace{})
}

// formats are the patterns of the phrases with verbs, %s, %v or %d, by phrase.
var formats sync.Map

// TrText is a text deck wrote in a language by words: a phrase as it is, or one with verbs whose values stay as they
// are, but for a value that is a word or a phrase of deck; the text itself when none fits. Only text deck wrote goes in.
func TrText(ws map[string]string, s string) string {
	if t := ws[s]; t != "" || s == "" {
		return cmp.Or(t, s)
	}
	keys := make([]string, 0, len(ws))
	for k := range ws {
		if strings.Contains(k, "%") {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	for _, k := range keys {
		m := phraseFormat(k).FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]any, len(m)-1)
		for i, v := range m[1:] {
			args[i] = Tr(ws, v)
			if v != s && args[i] == v {
				args[i] = TrText(ws, v)
			}
		}
		return fmt.Sprintf(verb.ReplaceAllString(ws[k], "%${1}s"), args...)
	}
	return s
}

// verb is a verb of a phrase, %s, %v, %d or %f, with its index and precision when it has them.
var verb = regexp.MustCompile(`%(\[\d+\])?(\.\d+)?[sdvf]`)

// phraseFormat is the pattern of phrase k: its verbs take any text, %d a number.
func phraseFormat(k string) *regexp.Regexp {
	if re, ok := formats.Load(k); ok {
		return re.(*regexp.Regexp)
	}
	parts := verb.Split(k, -1)
	verbs := verb.FindAllString(k, -1)
	var b strings.Builder
	b.WriteString("^")
	for i, p := range parts {
		b.WriteString(regexp.QuoteMeta(p))
		if i < len(verbs) {
			if strings.HasSuffix(verbs[i], "d") || strings.HasSuffix(verbs[i], "f") {
				b.WriteString(`(-?[\d.]+)`)
			} else {
				b.WriteString(`(.+?)`)
			}
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile("(?s)" + b.String())
	formats.Store(k, re)
	return re
}

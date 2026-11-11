// Package words is deck's dictionary: what deck says to people, by language and then by the English text.
package words

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

//go:embed *.json
var files embed.FS

var all = sync.OnceValue(func() map[string]map[string]string {
	out := map[string]map[string]string{}
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		panic(err)
	}
	for _, f := range names {
		raw, err := files.ReadFile(f)
		if err != nil {
			panic(err)
		}
		var ws map[string]string
		if err := json.Unmarshal(raw, &ws); err != nil {
			panic(f + ": " + err.Error())
		}
		out[strings.TrimSuffix(path.Base(f), ".json")] = ws
	}
	return out
})

// In are deck's words in a language by their English text; nil for English and a language deck has no words of.
func In(lang string) map[string]string {
	if ws, ok := all()[lang]; ok {
		return ws
	}
	base, _, _ := strings.Cut(lang, "-")
	return all()[base]
}

// Name is a language by its code in English, such as Russian for ru; the code itself when unknown, "" for none.
func Name(lang string) string {
	t, err := language.Parse(lang)
	if err != nil || lang == "" {
		return lang
	}
	if n := display.English.Tags().Name(t); n != "" {
		return n
	}
	return lang
}

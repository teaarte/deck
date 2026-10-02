package app

import (
	"strings"
	"text/template"

	"deck2/master"
)

var proseTemplates = template.Must(template.New("").ParseFS(master.Templates, "templates/*.md"))

// prose is a template of deck's own text for agents filled with data; data blocks come in already marked.
func prose(name string, data any) string {
	var b strings.Builder
	if err := proseTemplates.ExecuteTemplate(&b, name+".md", data); err != nil {
		panic("prose " + name + ": " + err.Error())
	}
	return b.String()
}

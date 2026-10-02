// Package master holds the built-in project master: its manifest, md prompt, command prompts, skills and the
// templates of deck's own text for its agents.
package master

import (
	"embed"
	"strings"
	"text/template"
)

//go:embed manifest.json prompt.md prompts skills
var FS embed.FS

// Templates are deck's own texts for agents, filled with data; they are not documents of the project.
//
//go:embed templates
var Templates embed.FS

// Name is the name of the built-in project.
const Name = "master"

var texts = template.Must(template.New("").ParseFS(Templates, "templates/*.md"))

// Text is a template of deck's own text for agents filled with data; data blocks come in already marked.
func Text(name string, data any) string {
	var b strings.Builder
	if err := texts.ExecuteTemplate(&b, name+".md", data); err != nil {
		panic("master text " + name + ": " + err.Error())
	}
	return b.String()
}

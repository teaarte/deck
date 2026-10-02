// Package master holds the built-in project master: its manifest, md prompt, command prompts, skills and the
// templates of deck's own text for its agents.
package master

import "embed"

//go:embed manifest.json prompt.md prompts skills
var FS embed.FS

// Templates are deck's own texts for agents, filled with data; they are not documents of the project.
//
//go:embed templates
var Templates embed.FS

// Name is the name of the built-in project.
const Name = "master"

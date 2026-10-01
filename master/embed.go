// Package master holds the built-in project master: its manifest, md prompt, command prompts and skills.
package master

import "embed"

//go:embed manifest.json prompt.md prompts skills
var FS embed.FS

// Name is the name of the built-in project.
const Name = "master"

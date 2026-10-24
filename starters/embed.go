// Package starters holds the modules deck offers on its first run, each a .zip as deck.project_export makes it.
package starters

import "embed"

//go:embed *.zip
var FS embed.FS

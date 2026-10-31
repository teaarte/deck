// Package starters holds the modules deck offers on its first run, a folder each as deck.project_export makes it.
package starters

import "embed"

//go:embed */*
var FS embed.FS

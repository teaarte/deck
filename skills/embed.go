// Package skills holds the AgentSkills skills built into deck; deck stores them as workspace skills.
package skills

import "embed"

//go:embed */*
var FS embed.FS

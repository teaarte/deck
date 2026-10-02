// Package home is the layout of deck's folder: state, which only deck reads, and work, where the programs deck
// confines work in their own folders.
package home

import (
	"fmt"
	"path/filepath"
)

// Layout is deck's folder.
type Layout string

func (l Layout) state(elem ...string) string {
	return filepath.Join(append([]string{string(l), "state"}, elem...)...)
}

func (l Layout) work(elem ...string) string {
	return filepath.Join(append([]string{string(l), "work"}, elem...)...)
}

// State holds everything only deck reads; nothing confined sees any of it.
func (l Layout) State() string { return l.state() }

// Work holds the folders of runs and the folders of projects.
func (l Layout) Work() string { return l.work() }

func (l Layout) DB() string        { return l.state("deck.db") }
func (l Layout) Token() string     { return l.state("token") }
func (l Layout) Files() string     { return l.state("files") }
func (l Layout) Backups() string   { return l.state("backups") }
func (l Layout) Browser() string   { return l.state("browser") }
func (l Layout) Tailscale() string { return l.state("tailscale") }
func (l Layout) Codex() string     { return l.state("codex") }
func (l Layout) ACP() string       { return l.state("acp") }

// Sessions holds the settings deck gives each session of the Claude Code CLI.
func (l Layout) Sessions() string { return l.state("mcp") }

// Secrets holds the secrets of a workspace, a file each.
func (l Layout) Secrets(workspace int64) string { return l.state("secrets", fmt.Sprint(workspace)) }

// Runs holds a folder per run of an agent or a script.
func (l Layout) Runs() string             { return l.work("runs") }
func (l Layout) Run(process int64) string { return l.work("runs", fmt.Sprint(process)) }

// Projects holds a folder per project: its data, which its scripts and agents keep, and the folders of its MCP servers.
func (l Layout) Projects() string             { return l.work("projects") }
func (l Layout) Project(project int64) string { return l.work("projects", fmt.Sprint(project)) }
func (l Layout) Data(project int64) string    { return l.work("projects", fmt.Sprint(project), "data") }

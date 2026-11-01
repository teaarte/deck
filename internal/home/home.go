// Package home is the layout of deck's folder: state, which only deck reads, and work, where the programs deck
// confines work in their own folders.
package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (l Layout) DB() string { return l.state("deck.db") }

// Update is the update a deck put in place and has not yet served in.
func (l Layout) Update() string { return l.state("update.json") }

// Lock is the file the one deck of a home holds locked while it runs.
func (l Layout) Lock() string  { return l.state("deck.lock") }
func (l Layout) Token() string { return l.state("token") }
func (l Layout) Files() string { return l.state("files") }

// File is a stored file by its id.
func (l Layout) File(id int64) string { return l.state("files", fmt.Sprint(id)) }
func (l Layout) Backups() string      { return l.state("backups") }

// Thumbs holds the light copies of the pictures of data folders.
func (l Layout) Thumbs() string { return l.state("thumbs") }

// SnapshotLayout is the time in the name of a snapshot in backups/, <kind>-<time>.tar.gz: SnapshotDaily for the
// daily and hand ones, SnapshotDelete for those before a project is deleted.
const (
	SnapshotLayout = "20060102T150405"
	SnapshotDaily  = "deck"
	SnapshotDelete = "deleted"
)

func (l Layout) Browser() string   { return l.state("browser") }
func (l Layout) Tailscale() string { return l.state("tailscale") }
func (l Layout) Codex() string     { return l.state("codex") }
func (l Layout) ACP() string       { return l.state("acp") }

// Doors holds the sockets of the doors of boxes, a folder each.
func (l Layout) Doors() string { return l.state("doors") }

// Agent is the home of the CLI of an agent for a project, its sessions and settings; project 0 is deck's own, of
// self-checks and the lists of models.
func (l Layout) Agent(provider string, project int64) string {
	return filepath.Join(string(l), "agents", provider, fmt.Sprint(project))
}

// Login is the folder of the one sign-in of the CLI of an agent, whose file every home of the CLI shares; no box
// writes it but through that file.
func (l Layout) Login(provider string) string {
	return filepath.Join(string(l), "agents", provider, "login")
}

// AgentLock is the folder every box of the CLI of an agent writes, its home inside: a lock the CLI takes beside its
// home, as of a refresh of the login, is one for all homes.
func (l Layout) AgentLock(provider string) string {
	return filepath.Join(string(l), "agents", provider, "lock")
}

// AgentTurns holds a folder a turn of the CLI of an agent, of what deck gives it: settings, MCP config, skills.
func (l Layout) AgentTurns(provider string) string {
	return filepath.Join(string(l), "agents", provider, "turns")
}

// PushKey is the key pair deck signs the pushes of a workspace with, apart from the secrets of the workspace.
func (l Layout) PushKey(workspace int64) string {
	return l.state("push", fmt.Sprintf("%d.vapid", workspace))
}

// Secrets holds the secrets of a workspace, a folder a project with a file a secret.
func (l Layout) Secrets(workspace int64) string { return l.state("secrets", fmt.Sprint(workspace)) }

// Keys holds the API keys of the connections of a workspace, a file each, apart from the secrets of modules.
func (l Layout) Keys(workspace int64) string { return l.state("keys", fmt.Sprint(workspace)) }

// Open makes deck's folder ready: its own and only deck's, of the current layout, with state/ and the folder of runs.
func Open(dir string) (Layout, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	l := Layout(dir)
	if old := l.Old(); len(old) > 0 {
		return "", fmt.Errorf("deck: %s holds a deck of an older layout (%s); move it away and start again, deck makes state/ and work/", dir, strings.Join(old, ", "))
	}
	for _, d := range []string{l.State(), l.Runs()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	return l, nil
}

// Old lists what deck kept in its folder before state/ and work/; deck does not start on such a folder.
func (l Layout) Old() []string {
	var out []string
	for _, n := range []string{"deck.db", "token", "secrets", "files", "projects", "runs", "backups", "mcp"} {
		if _, err := os.Stat(filepath.Join(string(l), n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// Runs holds a folder per run of an agent or a script: the files it hands back in results, its work and its log in
// stream.
func (l Layout) Runs() string                 { return l.work("runs") }
func (l Layout) Run(process int64) string     { return l.work("runs", fmt.Sprint(process)) }
func (l Layout) Results(process int64) string { return l.work("runs", fmt.Sprint(process), "results") }
func (l Layout) RunWork(process int64) string { return l.work("runs", fmt.Sprint(process), "work") }
func (l Layout) Stream(process int64) string  { return l.work("runs", fmt.Sprint(process), "stream") }

// Projects holds a folder per project: its data, which its scripts and agents keep, and the folders of its MCP servers.
func (l Layout) Projects() string             { return l.work("projects") }
func (l Layout) Project(project int64) string { return l.work("projects", fmt.Sprint(project)) }
func (l Layout) Data(project int64) string    { return l.work("projects", fmt.Sprint(project), "data") }

// MCP is the folder of an MCP server of a project, its home.
func (l Layout) MCP(project int64, server string) string {
	return l.work("projects", fmt.Sprint(project), "mcp", server)
}

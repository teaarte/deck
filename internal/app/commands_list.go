package app

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/provider"
	_ "embed"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
)

// commandRow is a command the caller may call, with its project, its file fields and what runs it: an executor
// of a command of the manifest, the provider an agent command runs on, or the kind of a tool, http or mcp.
type commandRow struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Files       []string `json:"files"`
	Executor    string   `json:"executor"`
	Provider    string   `json:"provider"`
	Tool        string   `json:"tool"`
	// Providers are the providers of the workspace an agent command may be set to run on.
	Providers []string `json:"providers,omitempty"`
}

type commandDescribeIn struct {
	Command string `desc:"Command name, such as deck.model_set or project.command"`
}

type commandSpec struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Class       string          `json:"class"`
	Callers     []string        `json:"callers"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// commandDescribe tells what a command takes whoever may call it, so the master can say what to fill in.
func (a *App) commandDescribe(ctx context.Context, act registry.Actor, in commandDescribeIn) (commandSpec, error) {
	c, err := a.Registry.Lookup(ctx, act.Workspace, in.Command)
	if err != nil {
		return commandSpec{}, err
	}
	return commandSpec{Name: c.Name, Title: c.Title, Description: c.Description, Class: c.Class, Callers: c.Callers,
		Input: c.Input, Result: c.Result}, nil
}

type commandsListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands of this project"`
}

// commandsList is what the caller may call: deck's commands and those of the projects it reaches.
func (a *App) commandsList(ctx context.Context, act registry.Actor, in commandsListIn) ([]commandRow, error) {
	pid, err := access.Filter(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	cmds, err := a.Registry.List(ctx, act)
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	manifests := map[int64]*manifest.Manifest{}
	var all []provider.Provider
	out := []commandRow{}
	for _, c := range cmds {
		if pid != 0 && c.ProjectID != pid {
			continue
		}
		r := commandRow{Name: c.Name, Title: c.Title, Description: c.Description, Project: names[c.ProjectID],
			Files: append([]string{}, process.FileFields(c.Input)...)}
		if c.ProjectID != 0 {
			m, ok := manifests[c.ProjectID]
			if !ok {
				if p, err := a.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
					m, _ = docs.Manifest(ctx, a.Store.Q, p)
				}
				manifests[c.ProjectID] = m
			}
			short := strings.TrimPrefix(c.Name, r.Project+".")
			switch {
			case m == nil:
			case m.Command(short) != nil:
				mc := m.Command(short)
				r.Executor = mc.Executor.Type
				if r.Executor != "agent" {
					break
				}
				if pv, err := a.Providers.For(ctx, c.ProjectID, mc); err == nil {
					r.Provider = pv.Name()
				}
				if all == nil {
					all, _ = a.Providers.All(ctx, act.Workspace)
				}
				r.Providers = []string{}
				for _, pv := range all {
					if len(pv.Caps().Missing(mc.Needs())) == 0 {
						r.Providers = append(r.Providers, pv.Name())
					}
				}
			case slices.ContainsFunc(m.Tools.HTTP, func(t manifest.HTTPTool) bool { return t.Name == short }):
				r.Tool = "http"
			case c.Fingerprint != nil:
				r.Tool = "mcp"
			}
		}
		out = append(out, r)
	}
	return out, nil
}

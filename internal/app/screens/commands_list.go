package screens

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// commandRow is a command the caller may call, with its project, its file fields and what runs it: an executor
// of a command of the manifest with the model of its work, or the kind of a tool, http or mcp.
type commandRow struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Project     string   `json:"project"`
	Files       []string `json:"files"`
	Executor    string   `json:"executor"`
	Tool        string   `json:"tool"`
	// OwnerOnly marks a command the master sees but only the owner calls, by its form at Form: a screen address,
	// <project> standing for the project, that FormParams after ? fill in.
	OwnerOnly  bool     `json:"owner_only,omitempty"`
	Form       string   `json:"form,omitempty"`
	FormParams []string `json:"form_params,omitempty"`
	// Work is the model that does the work of an agent or a model command, with the models that could.
	provider.Work
	// Cron is the schedule of its first cron trigger, Trigger that trigger's id.
	Cron      string `json:"cron,omitempty"`
	Trigger   int64  `json:"trigger,omitempty"`
	OwnPaused bool   `json:"own_paused,omitempty"`
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
func (ss *Service) commandDescribe(ctx context.Context, act registry.Actor, in commandDescribeIn) (commandSpec, error) {
	c, err := ss.Registry.Lookup(ctx, act.Workspace, in.Command)
	if err != nil {
		return commandSpec{}, err
	}
	return commandSpec{Name: c.Name, Title: c.Title, Description: c.Description, Class: c.Class, Callers: c.Callers,
		Input: c.Input, Result: c.Result}, nil
}

type commandsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name to filter by, or deck for built-in commands; omit for all available commands"`
	Lang    string `opt:"" desc:"Language of the prices of their models, such as ru; English without one"`
}

// commandsList is what the caller may call: deck's commands and those of the projects it reaches.
func (ss *Service) commandsList(ctx context.Context, act registry.Actor, in commandsListIn) ([]commandRow, error) {
	builtin := in.Project == registry.Builtin
	project := in.Project
	if builtin {
		project = ""
	}
	pid, err := access.Filter(ctx, ss.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	cmds, err := ss.Registry.List(ctx, act)
	if err != nil {
		return nil, err
	}
	master := act.Kind == registry.Master
	var ownerOnly map[string]bool
	if master {
		owner, err := ss.Registry.List(ctx, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace})
		if err != nil {
			return nil, err
		}
		ownerOnly = map[string]bool{}
		for _, c := range owner {
			if !slices.ContainsFunc(cmds, func(m *registry.Command) bool { return m.Name == c.Name }) {
				ownerOnly[c.Name] = true
				cmds = append(cmds, c)
			}
		}
		slices.SortFunc(cmds, func(x, y *registry.Command) int { return strings.Compare(x.Name, y.Name) })
	}
	names, err := access.Names(ctx, ss.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	ts, err := ss.St.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace, ProjectID: pid})
	if err != nil {
		return nil, err
	}
	work := ss.Works(ctx, act.Workspace, in.Lang)
	manifests := map[int64]*manifest.Manifest{}
	out := []commandRow{}
	for _, c := range cmds {
		if builtin && c.ProjectID != 0 || pid != 0 && c.ProjectID != pid {
			continue
		}
		r := commandRow{Name: c.Name, Title: c.Title, Project: names[c.ProjectID], Files: append([]string{}, manifest.FileFields(c.Input)...)}
		if !master {
			r.Description = c.Description
		}
		var m *manifest.Manifest
		if c.ProjectID != 0 {
			var ok bool
			if m, ok = manifests[c.ProjectID]; !ok {
				if p, err := ss.St.Q.GetProject(ctx, c.ProjectID); err == nil {
					m, _ = docs.Manifest(ctx, ss.St.Q, p)
				}
				manifests[c.ProjectID] = m
			}
		}
		if in.Lang != "" {
			ws := wordsOf(in.Lang, m)
			r.Title = manifest.Tr(ws, r.Title)
			if r.Description != "" {
				r.Description = manifest.Tr(ws, r.Description)
			}
		}
		if ownerOnly[c.Name] {
			r.OwnerOnly = true
			r.Form, r.FormParams = formOf(c.Name, r.Project, m)
		}
		if c.ProjectID != 0 {
			short := strings.TrimPrefix(c.Name, r.Project+".")
			switch {
			case m == nil:
			case m.Command(short) != nil:
				mc := m.Command(short)
				r.Executor = mc.Executor.Type
				if i := slices.IndexFunc(ts, func(t db.Trigger) bool { return t.ProjectID == c.ProjectID && t.Command == short && t.Kind == "cron" }); i >= 0 {
					r.Cron, r.Trigger, r.OwnPaused = ts[i].Spec, ts[i].ID, ts[i].Paused == 1
				}
				if r.Executor == "agent" || r.Executor == "model" {
					r.Work = work(c.ProjectID, short)
				}
			case slices.ContainsFunc(m.Tools.HTTP, func(t manifest.HTTPTool) bool { return t.Name == short }):
				r.Tool = "http"
			case c.Gate.Fingerprint != nil:
				r.Tool = "mcp"
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// formOf is the address of the first form off a record page that calls a command, with the parameters that fill it:
// in the screens of its project, then deck's, Settings first, then those of every project; empty when none has one.
func formOf(command, project string, m *manifest.Manifest) (string, []string) {
	type screen struct {
		at, command string
		s           *manifest.Screen
	}
	var all []screen
	if m != nil {
		for i := range m.Screens {
			all = append(all, screen{"#/p/" + project + "/", strings.TrimPrefix(command, project+"."), &m.Screens[i]})
		}
	}
	deck := shipped().Screens
	for i := range deck {
		if deck[i].Name == "settings" {
			all = append(all, screen{"#/deck/", command, &deck[i]})
		}
	}
	for i := range deck {
		if deck[i].Name != "settings" {
			all = append(all, screen{"#/deck/", command, &deck[i]})
		}
	}
	for i := range shipped().Project {
		all = append(all, screen{"#/p/" + cmp.Or(project, "<project>") + "/", command, &shipped().Project[i]})
	}
	for _, s := range all {
		if at, params, ok := manifest.FormAddress(s.at, s.s, s.command); ok {
			return at, params
		}
	}
	return "", nil
}

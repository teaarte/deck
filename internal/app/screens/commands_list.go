package screens

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// commandRow is a command the caller may call, with its project, its file fields and what runs it: an executor
// of a command of the manifest with the model of its work, or the kind of a tool, http or mcp.
type commandView struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Project     string   `json:"project"`
	Files       []string `json:"files"`
	Executor    string   `json:"executor"`
	Tool        string   `json:"tool"`
	// Document is the module's file its script or prompt is, which its page shows.
	Document int64 `json:"document,omitempty"`
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

// commandSpecView is what a command takes and gives; of an agent or a model command also the model of its work with the
// models it could take.
type commandSpecView struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Class       string          `json:"class"`
	Callers     []string        `json:"callers"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
	Executor    string          `json:"executor,omitempty"`
	provider.Work
}

// commandDescribe tells what a command takes whoever may call it, so the master can say what to fill in.
func (s *Service) commandDescribe(ctx context.Context, act registry.Actor, in commandDescribeIn) (commandSpecView, error) {
	c, err := s.lookup(ctx, act.Workspace, in.Command)
	if err != nil {
		return commandSpecView{}, err
	}
	out := commandSpecView{Name: c.Name, Title: c.Title, Description: c.Description, Class: c.Class, Callers: c.Callers,
		Input: c.Input, Result: c.Result}
	if c.of != nil {
		out.Executor = c.of.Executor.Type
		if out.Executor == "agent" || out.Executor == "model" {
			out.Work = s.Works(ctx, act.Workspace, "")(c.ProjectID, c.short)
		}
	}
	return out, nil
}

type commandsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name to filter by, or deck for built-in commands; omit for all available commands"`
	Lang    string `opt:"" desc:"Language of the prices of their models, such as ru; English without one"`
}

// commandsList is what the caller may call: deck's commands and those of the projects it reaches.
func (s *Service) commandsList(ctx context.Context, act registry.Actor, in commandsListIn) ([]commandView, error) {
	builtin := in.Project == registry.Builtin
	project := in.Project
	if builtin {
		project = ""
	}
	pid, err := access.Filter(ctx, s.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	cmds, err := s.Registry.List(ctx, act)
	if err != nil {
		return nil, err
	}
	master := act.Kind == registry.Master
	var ownerOnly map[string]bool
	if master {
		owner, err := s.Registry.List(ctx, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace})
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
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	ts, err := s.St.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace, ProjectID: pid})
	if err != nil {
		return nil, err
	}
	work := s.Works(ctx, act.Workspace, registry.Lang(ctx))
	manifests := map[int64]*manifest.Manifest{}
	projects := map[int64]db.Project{}
	out := []commandView{}
	for _, c := range cmds {
		if builtin && c.ProjectID != 0 || pid != 0 && c.ProjectID != pid {
			continue
		}
		r := commandView{Name: c.Name, Title: c.Title, Project: names[c.ProjectID], Files: append([]string{}, manifest.FileFields(c.Input)...)}
		if !master {
			r.Description = c.Description
		}
		var m *manifest.Manifest
		if c.ProjectID != 0 {
			var ok bool
			if m, ok = manifests[c.ProjectID]; !ok {
				if p, err := s.St.Q.GetProject(ctx, c.ProjectID); err == nil {
					m, _ = docs.Manifest(ctx, s.St.Q, p)
					projects[c.ProjectID] = p
				}
				manifests[c.ProjectID] = m
			}
		}
		if registry.Lang(ctx) != "" {
			ws := wordsOf(registry.Lang(ctx), m)
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
				file := mc.Executor.PromptFile
				if sc := m.Script(mc.Executor.Script); sc != nil {
					file = sc.File
				}
				if file != "" && !master {
					if d, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: docs.ProjectScope(projects[c.ProjectID]), Name: file}); err == nil {
						r.Document = d.ID
					}
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

func (s *Service) commandsListCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("commands_list", "Commands",
			"Lists the commands the caller may call, deck's and those of projects, with their file fields and what runs them; to the master also those only the owner calls, with the address of their form. project deck selects built-in commands; omit project for all commands. The model of a command, its choices and price, its schedule and the document of its script or prompt only by fields (model, models, choices, choice_groups, price, why, role, cron, trigger, own_paused, document); command_describe gives its input.",
			registry.OwnerSide, s.commandsList).On(events.Projects, events.Tools, events.Assignments, events.Models, events.Triggers, events.Health, events.Documents).
			Brief("name", "title", "description", "project", "files", "executor", "tool", "owner_only", "form", "form_params"),
		registry.Read("command_describe", "Describe command",
			"Gives the parameters of any command of the workspace, also of one only the owner calls, such as the forms of Settings: its input and result schemas, class and callers; of an agent or a model command also the model of its work, the models it could take (choices, choice_groups) and the price.",
			registry.OwnerSide, s.commandDescribe).On(events.Projects, events.Documents, events.Assignments, events.Models, events.Health),
	}
}

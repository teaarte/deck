package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Roles are the kinds of work an assignment names models for: agent turns, and direct calls by kind.
const (
	RoleAgent = "agent"
	RoleText  = "text"
	RoleImage = "image"
	RoleTTS   = "tts"
	RoleSTT   = "stt"
	RoleMusic = "music"
	RoleVideo = "video"
	// RoleSearch is web search for the agents that have none of their own.
	RoleSearch = "search"
)

var roles = []string{RoleAgent, RoleText, RoleImage, RoleTTS, RoleSTT, RoleMusic, RoleVideo, RoleSearch}

// Assigned are the models of a role for a command, in order: its own assignment, else its project's, else the
// workspace's; none when nothing is assigned.
func Assigned(ctx context.Context, q *db.Queries, workspace, project int64, command, role string) ([]string, error) {
	type place struct {
		project int64
		command string
	}
	places := []place{{0, ""}}
	if project != 0 {
		places = append([]place{{project, ""}}, places...)
		if command != "" {
			places = append([]place{{project, command}}, places...)
		}
	}
	for _, at := range places {
		raw, err := q.GetAssignment(ctx, db.GetAssignmentParams{WorkspaceID: workspace, ProjectID: at.project, Command: at.command, Role: role})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var names []string
		if json.Unmarshal([]byte(raw), &names) == nil && len(names) > 0 {
			return names, nil
		}
	}
	return nil, nil
}

type assignmentView struct {
	Project string   `json:"project,omitempty"`
	Command string   `json:"command,omitempty"`
	Role    string   `json:"role"`
	Models  []string `json:"models"`
	// Using is the model that takes the work now: the first that is up, a fallback when the first is down.
	Using string `json:"using,omitempty"`
	// Why is one line of the health of the first model that is down, when another takes the work.
	Why string `json:"why,omitempty"`
	// Choices are the models of deck that could do the work, Price how the price of the first reads.
	Choices []string `json:"choices"`
	Price   string   `json:"price"`
}

type assignmentSetIn struct {
	Role    string   `enum:"agent|text|image|tts|stt|music|video|search" desc:"The work: agent turns, direct calls of text, image, tts (speech), stt (speech to text), music or video, or web search for agents without their own"`
	Project string   `type:"project" opt:"" desc:"Project the assignment is for; the whole workspace without it"`
	Command string   `opt:"" desc:"Command of the project the assignment is for; the whole project without it"`
	Models  []string `opt:"" desc:"Names of models or built-in providers in order: the first, then the fallbacks; empty removes the assignment"`
	Model   string   `opt:"" desc:"One model to go first instead of models: the ones in force here before stay after it as fallbacks"`
}

func (s *Service) assignmentSet(ctx context.Context, act registry.Actor, in assignmentSetIn) (any, error) {
	project, models, err := s.planned(ctx, act.Workspace, in)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if len(models) == 0 {
			return q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: act.Workspace, ProjectID: project, Command: in.Command, Role: in.Role})
		}
		raw, _ := json.Marshal(models)
		return q.SetAssignment(ctx, db.SetAssignmentParams{WorkspaceID: act.Workspace, ProjectID: store.ID(project), Command: in.Command, Role: in.Role, Models: string(raw)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"role": in.Role, "project": in.Project, "command": in.Command, "models": models}, nil
}

// planned is the project of an assignment and the models it sets, checked; it changes nothing.
func (s *Service) planned(ctx context.Context, workspace int64, in assignmentSetIn) (int64, []string, error) {
	project, cmd, err := s.place(ctx, workspace, in)
	if err != nil {
		return 0, nil, err
	}
	if in.Model != "" {
		before, err := s.before(ctx, workspace, project, in.Command, in.Role, cmd)
		if err != nil {
			return 0, nil, err
		}
		in.Models = picked(in.Model, before)
	}
	for _, name := range in.Models {
		if in.Role == RoleAgent {
			pv, err := s.Provider(ctx, workspace, name)
			if err != nil {
				return 0, nil, err
			}
			if cmd == nil {
				continue
			}
			if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
				return 0, nil, registry.Invalid("deck: provider %s does not fit %s.%s: it lacks %s", name, in.Project, in.Command, strings.Join(missing, ", "))
			}
			continue
		}
		row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
		if err != nil {
			return 0, nil, registry.NotFound("deck: no model %q", name)
		}
		if row.Connection.Api == APICLI {
			return 0, nil, registry.Invalid("deck: model %s runs agent turns in %s; assign it to agent commands", name, row.Model.Agent)
		}
		if row.Model.Kind != in.Role {
			return 0, nil, registry.Invalid("deck: model %s is of kind %s, not %s", name, row.Model.Kind, in.Role)
		}
	}
	return project, slices.Compact(slices.Clone(in.Models)), nil
}

// place is the project of an assignment and, for agent turns of a command, the command.
func (s *Service) place(ctx context.Context, workspace int64, in assignmentSetIn) (int64, *manifest.Command, error) {
	if in.Command != "" && in.Project == "" {
		return 0, nil, registry.Invalid("deck: an assignment of a command names its project too")
	}
	var project int64
	if in.Project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: in.Project})
		if err != nil {
			return 0, nil, registry.NotFound("deck: no project %q", in.Project)
		}
		project = p.ID
	}
	if in.Role != RoleAgent || in.Command == "" {
		return project, nil, nil
	}
	_, cmd, err := s.agentCommand(ctx, workspace, in.Project+"."+in.Command)
	return project, cmd, err
}

// picked is one model first with the ones before it after it.
func picked(model string, before []string) []string {
	return append([]string{model}, slices.DeleteFunc(slices.Clone(before), func(n string) bool { return n == model })...)
}

// assignmentCommand is assignment_set: the owner's call sets the models at once; one of the master or a module waits
// for the owner's yes, which applies it only while the models in force there are still those the owner saw.
func (s *Service) assignmentCommand() *registry.Command {
	c := registry.Def("assignment_set", "Assign models",
		"Sets the models that do a kind of work in the workspace, a project or a command: the first, then the fallbacks a new turn or call takes when it is down. A module or the master proposes an assignment this way, of any project, and the owner applies it with one button.",
		registry.Everyone, s.assignmentSet).As(registry.External)
	c.Allowed = func(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
		in, err := assignmentInput(input)
		if err == nil {
			_, _, err = s.planned(ctx, act.Workspace, in)
		}
		return false, err
	}
	c.Free = func(context.Context, registry.Actor, json.RawMessage) (bool, error) {
		return false, registry.Refuse("deck: a trial or a draft does not change the models of deck; propose the assignment once the module is enabled")
	}
	c.Fingerprint = func(ctx context.Context, workspace, _ int64, input json.RawMessage) string {
		in, err := assignmentInput(input)
		if err != nil {
			return ""
		}
		project, cmd, err := s.place(ctx, workspace, in)
		if err != nil {
			return ""
		}
		before, err := s.before(ctx, workspace, project, in.Command, in.Role, cmd)
		if err != nil {
			return ""
		}
		raw, _ := json.Marshal(append([]string{}, before...))
		return string(raw)
	}
	c.Target = func(print string, input json.RawMessage) string {
		in, err := assignmentInput(input)
		if err != nil {
			return ""
		}
		var before []string
		_ = json.Unmarshal([]byte(print), &before)
		after := slices.Compact(slices.Clone(in.Models))
		if in.Model != "" {
			after = picked(in.Model, before)
		}
		where := "the workspace"
		switch {
		case in.Command != "":
			where = in.Project + "." + in.Command
		case in.Project != "":
			where = in.Project
		}
		return fmt.Sprintf("%s of %s: %s → %s", in.Role, where, listed(before, "deck's default"), listed(after, "none, the default above it"))
	}
	return c
}

func assignmentInput(input json.RawMessage) (assignmentSetIn, error) {
	var in assignmentSetIn
	err := json.Unmarshal(input, &in)
	return in, err
}

func listed(names []string, none string) string {
	if len(names) == 0 {
		return none
	}
	return strings.Join(names, ", ")
}

// before are the models a pick of one keeps after it: those in force at its place, else the model an agent command
// runs on now; the ones that do not fit the command drop out.
func (s *Service) before(ctx context.Context, workspace, project int64, command, role string, cmd *manifest.Command) ([]string, error) {
	names, err := Assigned(ctx, s.St.Q, workspace, project, command, role)
	if err != nil || cmd == nil {
		return names, err
	}
	if len(names) == 0 {
		if pv, err := s.For(ctx, project, cmd); err == nil {
			names = []string{pv.Name()}
		}
	}
	return slices.DeleteFunc(names, func(name string) bool {
		pv, err := s.Provider(ctx, workspace, name)
		return err != nil || len(pv.Caps().Missing(cmd.Requires)) > 0
	}), nil
}

// Unassigned are the commands of a manifest that miss a model, by name, with what the owner does: an agent command no
// provider fits or that calls deck.web_search on an ACP agent without a search, a model command with nothing assigned.
func (s *Service) Unassigned(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string {
	out := map[string]string{}
	for _, c := range m.Commands {
		if c.Executor.Type == "agent" {
			var refused *registry.Error
			_, fits, _, err := s.fitting(ctx, project, &c)
			if errors.As(err, &refused) {
				out[c.Name] = refused.Text
			} else if err == nil && !m.NoNetwork && c.Executor.Calls != nil && registry.Permits(c.Executor.Calls, registry.Builtin+".web_search") {
				// The first model that fits, as health tells of the assignment and not of who is up now.
				search, _ := Assigned(ctx, s.St.Q, workspace, project, c.Name, RoleSearch)
				if pv := fits[0]; len(search) == 0 && s.inAgent(ctx, workspace, pv.Name()) {
					out[c.Name] = "it calls deck.web_search and runs on " + pv.Name() + ", whose web is deck's: " + noSearch
				}
			}
			continue
		}
		if c.Executor.Type != "model" {
			continue
		}
		role, ok := strings.CutPrefix(c.Executor.Model, "@")
		if c.Executor.Model == "" {
			role, ok = c.Executor.Kind, true
		}
		if !ok || role == "" {
			continue
		}
		if names, err := Assigned(ctx, s.St.Q, workspace, project, c.Name, role); err == nil && len(names) == 0 {
			out[c.Name] = fmt.Sprintf("it calls @%s, but no model is assigned to %s; pick one in Settings → Commands", role, role)
		}
	}
	return out
}

// NoSearch is what the owner does while the workspace has no search assigned, or "".
func (s *Service) NoSearch(ctx context.Context, workspace int64) string {
	if names, err := Assigned(ctx, s.St.Q, workspace, 0, "", RoleSearch); err != nil || len(names) > 0 {
		return ""
	}
	return noSearch
}

// inAgent says a provider is a model in an ACP agent, whose web is deck's.
func (s *Service) inAgent(ctx context.Context, workspace int64, name string) bool {
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	return err == nil && row.Model.Agent != "" && row.Connection.Api != APICLI
}

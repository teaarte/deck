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
	"deck2/internal/provider"
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
	if in.Command != "" && in.Project == "" {
		return nil, registry.Invalid("deck: an assignment of a command names its project too")
	}
	var project int64
	if in.Project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: in.Project})
		if err != nil {
			return nil, registry.NotFound("deck: no project %q", in.Project)
		}
		project = p.ID
	}
	var cmd *manifest.Command
	if in.Role == RoleAgent && in.Command != "" {
		var err error
		if _, cmd, err = s.agentCommand(ctx, act.Workspace, in.Project+"."+in.Command); err != nil {
			return nil, err
		}
	}
	if in.Model != "" {
		before, err := s.before(ctx, act.Workspace, project, in.Command, in.Role, cmd)
		if err != nil {
			return nil, err
		}
		in.Models = append([]string{in.Model}, slices.DeleteFunc(before, func(n string) bool { return n == in.Model })...)
	}
	for _, name := range in.Models {
		if in.Role == RoleAgent {
			pv, err := s.Provider(ctx, act.Workspace, name)
			if err != nil {
				return nil, err
			}
			if cmd == nil {
				continue
			}
			if missing := pv.Caps().Missing(cmd.Needs()); len(missing) > 0 {
				return nil, registry.Invalid("deck: provider %s does not fit %s.%s: it lacks %s", name, in.Project, in.Command, strings.Join(missing, ", "))
			}
			continue
		}
		row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: name})
		if err != nil {
			return nil, registry.NotFound("deck: no model %q", name)
		}
		if row.Model.Kind != in.Role {
			return nil, registry.Invalid("deck: model %s is of kind %s, not %s", name, row.Model.Kind, in.Role)
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if len(in.Models) == 0 {
			return q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: act.Workspace, ProjectID: project, Command: in.Command, Role: in.Role})
		}
		models, _ := json.Marshal(slices.Compact(in.Models))
		return q.SetAssignment(ctx, db.SetAssignmentParams{WorkspaceID: act.Workspace, ProjectID: store.ID(project), Command: in.Command, Role: in.Role, Models: string(models)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"role": in.Role, "project": in.Project, "command": in.Command, "models": in.Models}, nil
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
		return err != nil || len(pv.Caps().Missing(cmd.Needs())) > 0
	}), nil
}

// Unassigned are the commands of a manifest that miss a model, by name, with what the owner does: an agent command no
// provider fits or that lacks deck's web on an ACP agent, or a model command whose role has nothing assigned.
func (s *Service) Unassigned(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string {
	out := map[string]string{}
	for _, c := range m.Commands {
		if c.Executor.Type == "agent" {
			var refused *registry.Error
			if _, _, _, err := s.fitting(ctx, project, &c); errors.As(err, &refused) {
				out[c.Name] = refused.Text
			} else if !m.NoNetwork && slices.Contains(c.Requires, provider.Web) {
				search, _ := Assigned(ctx, s.St.Q, workspace, project, c.Name, RoleSearch)
				pv, err := s.For(ctx, project, &c)
				switch {
				case err != nil || !s.inAgent(ctx, workspace, pv.Name()):
				case c.Executor.Calls != nil && !registry.Permits(c.Executor.Calls, registry.Builtin+".web_search") &&
					!registry.Permits(c.Executor.Calls, registry.Builtin+".web_fetch"):
					out[c.Name] = "it needs the web and runs on " + pv.Name() + ", whose web is deck's, but its calls list neither deck.web_search nor deck.web_fetch"
				case len(search) == 0:
					out[c.Name] = "it needs the web and runs on " + pv.Name() + ", whose web is deck's: " + noSearch
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
	return err == nil && row.Model.Agent != ""
}

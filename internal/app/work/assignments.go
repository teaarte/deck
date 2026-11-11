package work

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/models"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Roles are the kinds of work an assignment names models for: agent turns, and direct calls by kind.
const (
	RoleAgent = "agent"
	RoleText  = models.KindText
	RoleImage = models.KindImage
	RoleTTS   = models.KindTTS
	RoleSTT   = models.KindSTT
	RoleMusic = models.KindMusic
	RoleVideo = models.KindVideo
	// RoleSearch is web search for the agents that have none of their own.
	RoleSearch = models.KindSearch
	// RoleWidget is the agent that draws a view of the card of a module for a size; the master's model without one.
	RoleWidget = "widget"
)

var roles = []string{RoleAgent, RoleText, RoleImage, RoleTTS, RoleSTT, RoleMusic, RoleVideo, RoleSearch, RoleWidget}

// agentic says whether the models of a role run agent turns.
func agentic(role string) bool { return role == RoleAgent || role == RoleWidget }

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
	Role    string   `json:"role" enum:"agent|text|image|tts|stt|music|video|search|widget"`
	Models  []string `json:"models"`
	// Using is the model that takes the work now: the first that is up, a fallback when the first is down.
	Using string `json:"using,omitempty"`
	// Why is one line of the health of the first model that is down, when another takes the work.
	Why string `json:"why,omitempty"`
}

type assignmentSetIn struct {
	Role    string   `enum:"agent|text|image|tts|stt|music|video|search|widget" opt:"" desc:"The work: agent turns, direct calls of text, image, tts (speech), stt (speech to text), music or video, web search for agents without their own, or the agent that draws views of the cards of modules"`
	Project string   `type:"project" opt:"" desc:"Project the assignment is for; the whole workspace without it"`
	Command string   `opt:"" desc:"Command of the project the assignment is for; the whole project without it"`
	Models  []string `opt:"" desc:"Names of models or built-in providers in order: the first, then the fallbacks; empty removes the assignment"`
	Model   string   `opt:"" desc:"One model to go first instead of models: the ones in force here before stay after it as fallbacks"`
	// Assignments are several at once, one confirmation of the owner for all of them.
	Assignments string `type:"json" opt:"" desc:"Several assignments at once instead of the fields above, as a JSON list of {role, project, command, models or model}: a module or the master waits for one yes of the owner for all of them"`
}

// batch is the assignments of a call: its own fields, or the list of assignments; each names its role, and no place
// comes twice.
func batch(in assignmentSetIn) ([]assignmentSetIn, error) {
	if in.Assignments == "" {
		if in.Role == "" {
			return nil, registry.Invalid("give role, or assignments for several at once")
		}
		return []assignmentSetIn{in}, nil
	}
	if in.Role != "" || in.Project != "" || in.Command != "" || len(in.Models) > 0 || in.Model != "" {
		return nil, registry.Invalid("assignments come instead of role, project, command, models and model, not beside them")
	}
	var list []assignmentSetIn
	if err := json.Unmarshal([]byte(in.Assignments), &list); err != nil {
		return nil, registry.Invalid("assignments is a JSON list of {role, project, command, models or model}: %v", err)
	}
	if len(list) == 0 {
		return nil, registry.Invalid("assignments names none")
	}
	seen := map[[3]string]bool{}
	for _, a := range list {
		if a.Assignments != "" || !slices.Contains(roles, a.Role) {
			return nil, registry.Invalid("each of assignments names a role of %s and no assignments of its own", strings.Join(roles, ", "))
		}
		place := [3]string{a.Role, a.Project, a.Command}
		if seen[place] {
			return nil, registry.Invalid("assignments names %s of %s twice", a.Role, cmp.Or(strings.Trim(a.Project+"."+a.Command, "."), "the workspace"))
		}
		seen[place] = true
	}
	return list, nil
}

func (s *Service) assignmentSet(ctx context.Context, act registry.Actor, in assignmentSetIn) (any, error) {
	list, err := batch(in)
	if err != nil {
		return nil, err
	}
	type plan struct {
		project int64
		models  []string
	}
	plans := make([]plan, len(list))
	for i, a := range list {
		if plans[i].project, plans[i].models, err = s.planned(ctx, act.Workspace, a); err != nil {
			return nil, err
		}
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for i, a := range list {
			if len(plans[i].models) == 0 {
				if err := q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: act.Workspace, ProjectID: plans[i].project, Command: a.Command, Role: a.Role}); err != nil {
					return err
				}
				continue
			}
			raw, _ := json.Marshal(plans[i].models)
			if err := q.SetAssignment(ctx, db.SetAssignmentParams{WorkspaceID: act.Workspace, ProjectID: store.ID(plans[i].project), Command: a.Command, Role: a.Role, Models: string(raw)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var done []map[string]any
	var warnings []string
	for i, a := range list {
		done = append(done, map[string]any{"role": a.Role, "project": a.Project, "command": a.Command, "models": plans[i].models})
		warnings = append(warnings, s.pastLimits(ctx, act.Workspace, plans[i].project, a.Command, plans[i].models)...)
	}
	out := done[0]
	if in.Assignments != "" {
		out = map[string]any{"assignments": done}
	}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

// pastLimits warns of the models whose limits a usual request of the work is past: the median of its last calls.
func (s *Service) pastLimits(ctx context.Context, workspace, project int64, command string, models []string) []string {
	tokens, err := s.St.Q.RecentRequestTokens(ctx, db.RecentRequestTokensParams{WorkspaceID: workspace, ProjectID: project, Command: command})
	if err != nil || len(tokens) == 0 {
		return nil
	}
	slices.Sort(tokens)
	usual := tokens[len(tokens)/2]
	var out []string
	for _, name := range models {
		row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
		if err != nil {
			continue
		}
		switch m := row.Model; {
		case m.Context > 0 && usual > m.Context:
			out = append(out, fmt.Sprintf("%s holds %d tokens a request, and a usual request of this work is about %d: it goes to the next model", name, m.Context, usual))
		case m.Tpm > 0 && usual > m.Tpm:
			out = append(out, fmt.Sprintf("%s takes %d tokens a minute, and a usual request of this work is about %d: it goes to the next model", name, m.Tpm, usual))
		}
	}
	return out
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
		if agentic(in.Role) {
			pv, err := s.Provider(ctx, workspace, name)
			if err != nil {
				return 0, nil, err
			}
			if cmd == nil {
				continue
			}
			if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
				return 0, nil, registry.Invalid("provider %s does not fit %s.%s: it lacks %s", name, in.Project, in.Command, strings.Join(missing, ", "))
			}
			continue
		}
		row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
		if err != nil {
			return 0, nil, registry.NotFound("no model %q", name)
		}
		if row.Connection.Api == models.APICLI {
			return 0, nil, registry.Invalid("model %s runs agent turns in %s; assign it to agent commands", name, row.Model.Agent)
		}
		if row.Model.Kind != in.Role {
			return 0, nil, registry.Invalid("model %s is of kind %s, not %s", name, row.Model.Kind, in.Role)
		}
	}
	return project, slices.Compact(slices.Clone(in.Models)), nil
}

// place is the project of an assignment and, for agent turns of a command, the command; a removal needs none.
func (s *Service) place(ctx context.Context, workspace int64, in assignmentSetIn) (int64, *manifest.Command, error) {
	if in.Command != "" && in.Project == "" {
		return 0, nil, registry.Invalid("an assignment of a command names its project too")
	}
	var project int64
	if in.Project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: in.Project})
		if err != nil {
			return 0, nil, registry.NotFound("no project %q", in.Project)
		}
		project = p.ID
	}
	if in.Role != RoleAgent || in.Command == "" || len(in.Models) == 0 && in.Model == "" {
		return project, nil, nil
	}
	_, cmd, err := s.agentCommand(ctx, workspace, in.Project+"."+in.Command)
	return project, cmd, err
}

// picked is one model first with the ones before it after it.
func picked(model string, before []string) []string {
	return append([]string{model}, slices.DeleteFunc(slices.Clone(before), func(n string) bool { return n == model })...)
}

// assignmentCommands are assignment_set: the owner's call sets the models at once; one of the master or a module waits
// for the owner's yes, which applies it only while the models in force there are still those the owner saw.
func (s *Service) assignmentCommands() []*registry.Command {
	c := registry.Def("assignment_set", "Assign models",
		"Sets the models that do a kind of work in the workspace, a project or a command: the first, then the fallbacks a new turn or call takes when it is down. A module or the master proposes an assignment this way, of any project, and the owner applies it with one button.",
		registry.Everyone, s.assignmentSet).As(registry.External)
	c.Gate.Allowed = func(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
		list, err := assignmentInput(input)
		for _, in := range list {
			if _, _, err = s.planned(ctx, act.Workspace, in); err != nil {
				break
			}
		}
		return false, err
	}
	c.Gate.Free = func(context.Context, registry.Actor, json.RawMessage) (bool, error) {
		return false, registry.Refuse("a trial or a draft does not change the models of deck; propose the assignment once the module is enabled")
	}
	// The fingerprint is the models in force at the place before, a list of them for several assignments.
	c.Gate.Fingerprint = func(ctx context.Context, workspace, _ int64, input json.RawMessage) string {
		list, err := assignmentInput(input)
		if err != nil {
			return ""
		}
		befores := make([][]string, len(list))
		for i, in := range list {
			project, cmd, err := s.place(ctx, workspace, in)
			if err != nil {
				return ""
			}
			before, err := s.before(ctx, workspace, project, in.Command, in.Role, cmd)
			if err != nil {
				return ""
			}
			befores[i] = append([]string{}, before...)
		}
		var raw []byte
		if several(input) {
			raw, _ = json.Marshal(befores)
		} else {
			raw, _ = json.Marshal(befores[0])
		}
		return string(raw)
	}
	// An assignment holds when its very place has those models, not one it would inherit them from.
	c.Gate.Holds = func(ctx context.Context, workspace int64, input json.RawMessage) bool {
		list, err := assignmentInput(input)
		if err != nil {
			return false
		}
		for _, in := range list {
			if !s.holds(ctx, workspace, in) {
				return false
			}
		}
		return true
	}
	// The undo sets back the models of each very place, or removes the assignment the call made there.
	c.Gate.Undo = func(ctx context.Context, workspace int64, input json.RawMessage) (string, json.RawMessage, bool) {
		list, err := assignmentInput(input)
		if err != nil {
			return "", nil, false
		}
		backs := make([]map[string]any, len(list))
		for i, in := range list {
			project, _, err := s.place(ctx, workspace, in)
			if err != nil {
				return "", nil, false
			}
			prev := []string{}
			raw, err := s.St.Q.GetAssignment(ctx, db.GetAssignmentParams{WorkspaceID: workspace, ProjectID: project, Command: in.Command, Role: in.Role})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", nil, false
			}
			if err == nil {
				_ = json.Unmarshal([]byte(raw), &prev)
			}
			back := map[string]any{"role": in.Role, "models": prev}
			if in.Project != "" {
				back["project"] = in.Project
			}
			if in.Command != "" {
				back["command"] = in.Command
			}
			backs[i] = back
		}
		var b []byte
		if several(input) {
			all, _ := json.Marshal(backs)
			b, _ = json.Marshal(map[string]any{"assignments": string(all)})
		} else {
			b, _ = json.Marshal(backs[0])
		}
		return c.Name, b, true
	}
	c.Gate.Sets = func(input json.RawMessage) []string {
		list, _ := assignmentInput(input)
		out := make([]string, len(list))
		for i, in := range list {
			out[i] = in.Role + " " + in.Project + "." + in.Command
		}
		return out
	}
	c.Gate.Target = func(print string, input json.RawMessage) string {
		var lines []string
		for _, l := range assignmentLines(print, input) {
			lines = append(lines, l.text)
		}
		return strings.Join(lines, "\n")
	}
	c.Gate.Changes = func(print string, input json.RawMessage) []string {
		var lines []string
		for _, l := range assignmentLines(print, input) {
			if l.changes {
				lines = append(lines, l.text)
			}
		}
		return lines
	}
	return []*registry.Command{c}
}

// assignmentLine is a place an assignment sets as the owner reads it, with its models before and after, and whether
// they differ.
type assignmentLine struct {
	text    string
	changes bool
}

// assignmentLines are the places a call of assignment_set sets, with the models in force before as its fingerprint
// gives them.
func assignmentLines(print string, input json.RawMessage) []assignmentLine {
	list, err := assignmentInput(input)
	if err != nil {
		return nil
	}
	befores := make([][]string, len(list))
	if several(input) {
		_ = json.Unmarshal([]byte(print), &befores)
	} else {
		_ = json.Unmarshal([]byte(print), &befores[0])
	}
	lines := make([]assignmentLine, len(list))
	for i, in := range list {
		var before []string
		if i < len(befores) {
			before = befores[i]
		}
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
		lines[i] = assignmentLine{fmt.Sprintf("%s of %s: %s → %s", in.Role, where, listed(before, "deck's default"), listed(after, "none, the default above it")),
			!slices.Equal(before, after)}
	}
	return lines
}

func assignmentInput(input json.RawMessage) ([]assignmentSetIn, error) {
	var in assignmentSetIn
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	return batch(in)
}

// several says a call makes several assignments at once.
func several(input json.RawMessage) bool {
	var in struct{ Assignments string }
	return json.Unmarshal(input, &in) == nil && in.Assignments != ""
}

// holds says the very place of an assignment has the models it would set.
func (s *Service) holds(ctx context.Context, workspace int64, in assignmentSetIn) bool {
	project, cmd, err := s.place(ctx, workspace, in)
	if err != nil {
		return false
	}
	raw, err := s.St.Q.GetAssignment(ctx, db.GetAssignmentParams{WorkspaceID: workspace, ProjectID: project, Command: in.Command, Role: in.Role})
	var here []string
	if err != nil || json.Unmarshal([]byte(raw), &here) != nil {
		return false
	}
	after := slices.Compact(slices.Clone(in.Models))
	if in.Model != "" {
		before, err := s.before(ctx, workspace, project, in.Command, in.Role, cmd)
		if err != nil {
			return false
		}
		after = picked(in.Model, before)
	}
	return len(after) > 0 && slices.Equal(after, here)
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
				out[c.Name] = refused.Error()
			} else if err == nil && c.Executor.Calls != nil && registry.Permits(c.Executor.Calls, registry.Deck("web_search")) {
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
			out[c.Name] = fmt.Sprintf("it calls @%s, but no model is assigned to %s; pick one in Settings → Models", role, role)
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
	return err == nil && row.Model.Agent != "" && row.Connection.Api != models.APICLI
}

// agentCommand finds an agent command of the workspace by its place, project.command.
func (s *Service) agentCommand(ctx context.Context, workspace int64, place string) (db.Project, *manifest.Command, error) {
	name, command, _ := strings.Cut(place, ".")
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
	if err != nil {
		return p, nil, registry.NotFound("no project %q", name)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return p, nil, err
	}
	cmd := m.Command(command)
	if cmd == nil || cmd.Executor.Type != process.KindAgent {
		return p, nil, registry.Invalid("%s is not an agent command; only agent commands call a model", place)
	}
	return p, cmd, nil
}

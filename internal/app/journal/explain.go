package journal

import (
	"context"
	"encoding/json"

	"deck2/internal/access"
	"deck2/internal/events"
	"deck2/internal/registry"
)

type callExplainIn struct {
	Command string `desc:"The command or tool, as deck.model_call or notes.send"`
	Input   string `type:"json" opt:"" desc:"Its input as a JSON object; {} without one"`
	Project string `type:"project" opt:"" desc:"Whose call: an agent of this module; without it and process, the caller's own"`
	Process int64  `opt:"" desc:"Whose call: the turn of this process, with what the owner allowed its work"`
}

// explainView is how deck would decide a call now: the row of its table of decisions, what the row does, why, the
// facts it read, the right an answer goes to and what the call acts on.
type explainView struct {
	Row     string            `json:"row"`
	Does    string            `json:"does"`
	Text    string            `json:"text"`
	Facts   map[string]any    `json:"facts"`
	Need    *registry.Need    `json:"need,omitempty"`
	Subject *registry.Subject `json:"subject,omitempty"`
}

// callExplain says how deck would decide a call of a command now, by whom, without making it.
func (s *Service) callExplain(ctx context.Context, act registry.Actor, in callExplainIn) (explainView, error) {
	who := act
	switch {
	case in.Process != 0:
		p, err := s.St.Q.GetProcess(ctx, in.Process)
		if err != nil || !act.Within(p.ProjectID) {
			return explainView{}, registry.NotFound("no process %d", in.Process)
		}
		proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
		if err != nil || proj.WorkspaceID != act.Workspace {
			return explainView{}, registry.NotFound("no process %d", in.Process)
		}
		who = registry.Actor{Kind: registry.Agent, Workspace: act.Workspace, ProjectID: p.ProjectID, ProcessID: p.ID, Trial: p.Trial == 1}
		if proj.Builtin == 1 {
			who.Kind = registry.Master
		}
	case in.Project != "":
		p, err := access.Project(ctx, s.St.Q, act, in.Project)
		if err != nil {
			return explainView{}, err
		}
		who = registry.Actor{Kind: registry.Agent, Workspace: act.Workspace, ProjectID: p.ID}
		if p.Builtin == 1 {
			who.Kind = registry.Master
		}
	}
	input := json.RawMessage(in.Input)
	if in.Input == "" {
		input = json.RawMessage(`{}`)
	}
	v, err := s.Explain(ctx, who, in.Command, input)
	if err != nil {
		return explainView{}, err
	}
	facts := map[string]any{}
	b, _ := json.Marshal(v.Facts)
	_ = json.Unmarshal(b, &facts)
	out := explainView{Row: v.Name, Does: v.Does, Text: v.Text, Facts: facts, Need: v.Need}
	if v.Subject.Print != "" || v.Subject.Where != "" || v.Subject.Changes != nil {
		out.Subject = &v.Subject
	}
	return out, nil
}

// explainCommands are call_explain, the table of decisions as an answer.
func (s *Service) explainCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("call_explain", "Explain a call",
			"Says how deck would decide a call of a command now, without making it: the row of its one table of decisions (in_deck, owner, cleared, refused, draft, draft_tool, undeclared, denied, always, for_work, pressed, in_force, ask), what that row does (run, show, refuse, unchanged, ask), why, the facts deck read, the right of its catalog an answer goes to and what the call acts on.",
			registry.OwnerSide, s.callExplain).On(events.Projects, events.Processes),
	}
}

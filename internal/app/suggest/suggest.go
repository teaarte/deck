// Package suggest keeps suggestions: a card or a call an agent or the master proposes, which the owner allows,
// refuses or gives to another project, and which a project on autonomy auto carries out at once.
package suggest

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/internal/app/board"
	"deck2/internal/app/devices"
	"deck2/internal/events"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Service keeps suggestions. Days is how many days a suggestion of a workspace waits for the owner.
type Service struct {
	St       *store.Store
	Bus      *events.Bus
	Now      func() time.Time
	Registry *registry.Registry
	Gateway  *gateway.Gateway
	Asks     *ask.Broker
	Days     func(ctx context.Context, workspace int64) int
}

// cardSuggestion is the input of the deck.card_create a card suggestion freezes.
type cardSuggestion struct {
	Project string `json:"project"`
	Title   string `json:"title"`
	Text    string `json:"text,omitempty"`
	Record  int64  `json:"record,omitempty"`
}

// suggested is what a suggestion does, in one line as the owner and later suggesters read it.
func suggested(f gateway.Frozen) string {
	var c cardSuggestion
	if f.Call.Command == registry.Builtin+".card_create" && json.Unmarshal(f.Call.Input, &c) == nil {
		return fmt.Sprintf("card in %s: %s", c.Project, untrusted.Line(c.Title, 200))
	}
	return fmt.Sprintf("call %s with %s", f.Call.Command, untrusted.Cut(string(f.Call.Input), 300))
}

type suggestIn struct {
	Project string `type:"project" opt:"" desc:"Project of the card; the caller's own when omitted"`
	Title   string `opt:"" desc:"Card title, for a card"`
	Text    string `type:"text" opt:"" desc:"Card text, markdown"`
	Record  int64  `type:"record" opt:"" desc:"Record the card works on"`
	Command string `opt:"" desc:"Command to call instead, project.command"`
	Input   string `type:"json" opt:"" desc:"Its input: a JSON object"`
	Reason  string `desc:"Why, in one line the owner reads"`
}

func (sg *Service) suggest(ctx context.Context, act registry.Actor, in suggestIn) (any, error) {
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return nil, registry.Invalid("deck: a suggestion needs its reason, one line the owner reads")
	case (in.Title == "") == (in.Command == ""):
		return nil, registry.Invalid("deck: suggest either a card (title) or a call (command), one of them")
	}
	var f gateway.Frozen
	var target db.Project
	var err error
	class := registry.Internal
	if in.Command != "" {
		c, err := sg.Registry.Lookup(ctx, act.Workspace, in.Command)
		if err != nil {
			return nil, err
		}
		if c.ProjectID == 0 || c.Fingerprint != nil {
			return nil, registry.Invalid("deck: suggest calls of project commands; %s is not one", in.Command)
		}
		if !slices.Contains(c.Callers, registry.Owner) {
			return nil, registry.Invalid("deck: the owner makes what a suggestion asks for, and the owner cannot call %s", in.Command)
		}
		if target, err = sg.St.Q.GetProject(ctx, c.ProjectID); err != nil {
			return nil, err
		}
		input := json.RawMessage(`{}`)
		if strings.TrimSpace(in.Input) != "" {
			input = json.RawMessage(in.Input)
		}
		if _, err := c.Validate(input); err != nil {
			return nil, err
		}
		f, class = gateway.Freeze(act, in.Command, input), c.Class
	} else {
		if target, err = sg.suggestTarget(ctx, act, in.Project); err != nil {
			return nil, err
		}
		card, _ := json.Marshal(cardSuggestion{Project: target.Name, Title: strings.TrimSpace(in.Title), Text: in.Text, Record: in.Record})
		f = gateway.Freeze(act, registry.Builtin+".card_create", card)
	}
	f.As, f.Reason = gateway.AsOwner, untrusted.Cut(strings.Join(strings.Fields(reason), " "), 300)
	m, err := docs.Manifest(ctx, sg.St.Q, target)
	if err != nil {
		return nil, err
	}
	card := in.Command == ""
	if card && (m.Board == nil || m.RoleLane("inbox") == nil) {
		return nil, registry.Invalid("deck: project %s has no inbox lane for a card", target.Name)
	}
	if card && in.Record != 0 {
		if r, err := sg.St.Q.GetRecord(ctx, in.Record); err != nil || r.ProjectID != target.ID {
			return nil, registry.NotFound("deck: no record %d in %s", in.Record, target.Name)
		}
	}
	// Only the project's own agents, the master and the owner may have it done at once, the master only in its
	// project or for a call its list of calls allows; a trial never does.
	own := act.Within(target.ID) && (act.Kind != registry.Master || target.ID == act.ProjectID ||
		act.Commands == nil || registry.Permits(act.Commands, f.Call.Command))
	if m.Autonomy == "auto" && target.Status == store.ProjectEnabled && own && !act.Trial {
		return sg.doSuggestion(ctx, act, target, f)
	}
	expires := sg.Now().Add(time.Duration(sg.Days(ctx, target.WorkspaceID)) * 24 * time.Hour)
	var q db.Ask
	err = sg.St.Tx(ctx, func(ctx context.Context, tx *db.Queries) error {
		var err error
		q, err = gateway.Hold(ctx, tx, act, target.ID, ask.KindSuggest, class, f.Reason+"\n"+suggested(f), f, expires)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"suggestion": q.ID, "waiting": true, "expires_at": q.ExpiresAt.String}, nil
}

// suggestTarget is the project a card suggestion goes to: the one named, else the caller's own.
func (sg *Service) suggestTarget(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		return access.Project(ctx, sg.St.Q, act, "")
	}
	p, err := sg.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if err != nil {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	return p, nil
}

// doSuggestion carries a suggestion out in a project on autonomy auto and lists it in the feed; a call keeps the
// class rules of its command, and a card can be deleted again.
func (sg *Service) doSuggestion(ctx context.Context, act registry.Actor, p db.Project, f gateway.Frozen) (any, error) {
	row := db.AddFeedParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: devices.FeedChange, Author: act.Author(), Actions: "[]", Params: "{}",
		Text: fmt.Sprintf("%s suggested and deck did it: %s (%s)", act.Author(), suggested(f), f.Reason)}
	// The suggestion names the call, so the calls list of the suggesting command does not narrow it.
	act.Commands = nil
	out, err := sg.Gateway.CallAs(ctx, act, f.Call.Command, f.Call.Input)
	if err != nil {
		return nil, err
	}
	if w, held := out.(gateway.Waiting); held {
		return map[string]any{"done": false, "waiting": w.Ask, "text": w.Text}, nil
	}
	if c, ok := out.(board.Card); ok {
		params, _ := json.Marshal(map[string]any{"card": c.ID})
		row.Command, row.Params = registry.Builtin+".card_delete", string(params)
	}
	err = sg.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.AddFeed(ctx, row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"done": true, "result": out}, nil
}

// Answer settles a suggestion: yes makes the card or the call as the owner, no rejects it with the
// reason, move gives a card suggestion to another project, where it waits again.
func (sg *Service) Answer(ctx context.Context, act registry.Actor, q db.Ask, answer, project string) (any, error) {
	var f gateway.Frozen
	if err := json.Unmarshal([]byte(q.Payload), &f); err != nil {
		return nil, err
	}
	switch {
	case answer == "move":
		var c cardSuggestion
		if f.Call.Command != registry.Builtin+".card_create" || json.Unmarshal(f.Call.Input, &c) != nil {
			return nil, registry.Invalid("deck: only a card suggestion moves to another project")
		}
		p, err := sg.suggestTarget(ctx, act, strings.TrimSpace(project))
		if err != nil {
			return nil, err
		}
		if m, err := docs.Manifest(ctx, sg.St.Q, p); err != nil || m.RoleLane("inbox") == nil {
			return nil, registry.Invalid("deck: project %s has no inbox lane for a card", p.Name)
		}
		if c.Project != p.Name {
			c.Record = 0
		}
		c.Project = p.Name
		f.Call.Input, _ = json.Marshal(c)
		var n int64
		err = sg.St.Tx(ctx, func(ctx context.Context, tx *db.Queries) error {
			var err error
			n, err = tx.MoveSuggestion(ctx, db.MoveSuggestionParams{ProjectID: store.ID(p.ID), Text: f.Reason + "\n" + suggested(f), Payload: f.String(), ID: q.ID})
			if err != nil || n == 0 {
				return err
			}
			return tx.MoveAskCalls(ctx, db.MoveAskCallsParams{ProjectID: store.ID(p.ID), AskID: store.ID(q.ID)})
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ask.NotOpen
		}
		return map[string]any{"suggestion": q.ID, "project": p.Name}, nil
	case answer == "allow":
		out, err := sg.Gateway.Decide(ctx, act, q, answer, nil)
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, ask.NotOpen
		}
		return out, err
	case answer == "deny" || strings.HasPrefix(answer, "deny:"):
		if err := sg.Asks.Answer(ctx, q.ID, answer); err != nil {
			return nil, ask.NotOpen
		}
		sg.Gateway.Record(ctx, q, "rejected", "the owner said no")
		return map[string]any{"suggestion": q.ID, "rejected": true}, nil
	}
	return nil, registry.Invalid("deck: answer a suggestion with allow, deny, deny:<reason> or move with project")
}

type suggestionsRejectedIn struct {
	Days int64 `opt:"" desc:"How far back, at most and by default 30 days"`
}

// suggestionsRejected are the suggestions the owner said no to lately, so a suggester does not make them again;
// an agent sees those of its own project and those it made.
func (sg *Service) suggestionsRejected(ctx context.Context, act registry.Actor, in suggestionsRejectedIn) (any, error) {
	in.Days = registry.Limit(in.Days, 30, 30)
	since := store.Time(sg.Now().Add(-time.Duration(in.Days) * 24 * time.Hour))
	rows, err := sg.St.Q.ListRejectedSuggestions(ctx, db.ListRejectedSuggestionsParams{WorkspaceID: act.Workspace, Since: sql.NullString{String: since, Valid: true}})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, q := range rows {
		var f gateway.Frozen
		if json.Unmarshal([]byte(q.Payload), &f) != nil {
			continue
		}
		if !act.Within(q.ProjectID.Int64) && !sg.byProject(ctx, f.Caller.Process, act.ProjectID) {
			continue
		}
		why := strings.TrimPrefix(strings.TrimPrefix(q.Answer, "deny"), ":")
		p, _ := sg.St.Q.GetProject(ctx, q.ProjectID.Int64)
		out = append(out, map[string]any{"project": p.Name, "what": suggested(f), "reason": f.Reason, "why_not": untrusted.Cut(why, 500),
			"by": f.Caller.Author(), "answered_at": q.AnsweredAt.String})
	}
	return out, nil
}

// byProject reports whether a process is one of the project.
func (sg *Service) byProject(ctx context.Context, process, projectID int64) bool {
	if process == 0 {
		return false
	}
	p, err := sg.St.Q.GetProcess(ctx, process)
	return err == nil && p.ProjectID == projectID
}

// Expire closes suggestions the owner left unanswered past their term; that is not a refusal.
func (sg *Service) Expire(ctx context.Context, now time.Time) {
	_, err := store.TxGet(sg.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.ExpireSuggestions(ctx, store.Time(now))
	})
	if err != nil {
		slog.Error("expire suggestions", "err", err)
	}
}

// Commands are the commands of suggestions.
func (sg *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("suggest", "Suggest",
			"Suggests the owner one card for a project or one call of a project command, with the reason; it waits in waiting for you, or is done at once in a project on autonomy auto.",
			registry.Everyone, sg.suggest),
		registry.Def("suggestions_rejected", "Rejected suggestions",
			"Lists the suggestions the owner said no to lately, with the reason, so the same is not suggested again.",
			registry.Everyone, sg.suggestionsRejected),
	}
}

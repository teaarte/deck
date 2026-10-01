package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/app/docs"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// suggestion is the draft an ask of kind suggest holds: one card for a project or one call of a command.
type suggestion struct {
	Card   *suggestedCard `json:"card,omitempty"`
	Call   *suggestedCall `json:"call,omitempty"`
	Reason string         `json:"reason"`
	By     string         `json:"by"`
}

type suggestedCard struct {
	Project string `json:"project"`
	Title   string `json:"title"`
	Text    string `json:"text,omitempty"`
	Record  int64  `json:"record,omitempty"`
}

type suggestedCall struct {
	Command string          `json:"command"`
	Input   json.RawMessage `json:"input"`
}

// what is the draft in one line, as the owner and later suggesters read it.
func (s suggestion) what() string {
	if s.Card != nil {
		return fmt.Sprintf("card in %s: %s", s.Card.Project, untrusted.Line(s.Card.Title, 200))
	}
	return fmt.Sprintf("call %s with %s", s.Call.Command, untrusted.Cut(string(s.Call.Input), 300))
}

// suggestDays is how many days a suggestion waits for the owner before it expires.
func (a *App) suggestDays(ctx context.Context, workspace int64) int {
	if n := a.settingInt(ctx, workspace, settingSuggestDays); n > 0 {
		return n
	}
	return 7
}

func (a *App) suggest(ctx context.Context, act registry.Actor, in struct {
	Project, Title, Text, Command, Input, Reason string
	Record                                       int64
}) (any, error) {
	reason := strings.TrimSpace(in.Reason)
	switch {
	case reason == "":
		return nil, registry.Invalid("deck: a suggestion needs its reason, one line the owner reads")
	case (in.Title == "") == (in.Command == ""):
		return nil, registry.Invalid("deck: suggest either a card (title) or a call (command), one of them")
	}
	s := suggestion{Reason: untrusted.Cut(strings.Join(strings.Fields(reason), " "), 300), By: act.Author()}
	var target db.Project
	var err error
	if in.Command != "" {
		c, err := a.Registry.Lookup(ctx, act.Workspace, in.Command)
		if err != nil {
			return nil, err
		}
		if c.ProjectID == 0 {
			return nil, registry.Invalid("deck: suggest calls of project commands; %s is deck's own", in.Command)
		}
		if target, err = a.Store.Q.GetProject(ctx, c.ProjectID); err != nil {
			return nil, err
		}
		input := json.RawMessage(`{}`)
		if strings.TrimSpace(in.Input) != "" {
			input = json.RawMessage(in.Input)
		}
		if _, err := c.Validate(input); err != nil {
			return nil, err
		}
		s.Call = &suggestedCall{Command: in.Command, Input: json.RawMessage(gateway.Canonical(input))}
	} else {
		if target, err = a.suggestTarget(ctx, act, in.Project); err != nil {
			return nil, err
		}
		s.Card = &suggestedCard{Project: target.Name, Title: strings.TrimSpace(in.Title), Text: in.Text, Record: in.Record}
	}
	m, err := docs.Manifest(ctx, a.Store.Q, target)
	if err != nil {
		return nil, err
	}
	if s.Card != nil && (m.Board == nil || m.RoleLane("inbox") == nil) {
		return nil, registry.Invalid("deck: project %s has no inbox lane for a card", target.Name)
	}
	if m.Autonomy == "auto" && target.Status == store.ProjectEnabled {
		return a.doSuggestion(ctx, act, target, s)
	}
	payload, _ := json.Marshal(s)
	expires := a.Cfg.Now().Add(time.Duration(a.suggestDays(ctx, target.WorkspaceID)) * 24 * time.Hour)
	var q db.Ask
	err = a.Store.Tx(ctx, func(ctx context.Context, tx *db.Queries) error {
		var err error
		q, err = tx.CreateSuggestion(ctx, db.CreateSuggestionParams{WorkspaceID: target.WorkspaceID, ProjectID: target.ID,
			Text: s.Reason + "\n" + s.what(), Payload: string(payload), ExpiresAt: ask.At(expires)})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("asks")
	return map[string]any{"suggestion": q.ID, "waiting": true, "expires_at": q.ExpiresAt.String}, nil
}

// suggestTarget is the project a card suggestion goes to: the one named, else the caller's own.
func (a *App) suggestTarget(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		return a.scope(ctx, act, "")
	}
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if err != nil {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	return p, nil
}

// doSuggestion carries a suggestion out in a project on autonomy auto and lists it in done without you; a
// call keeps the class rules of its command.
func (a *App) doSuggestion(ctx context.Context, act registry.Actor, p db.Project, s suggestion) (any, error) {
	var out any
	row := db.AddUnattendedParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Author: s.By, Params: "{}",
		Text: fmt.Sprintf("%s suggested and deck did it: %s (%s)", s.By, s.what(), s.Reason)}
	if s.Card != nil {
		v, err := a.suggestedCard(ctx, owner(p.WorkspaceID), s.Card)
		if err != nil {
			return nil, err
		}
		out = v
		params, _ := json.Marshal(map[string]any{"card": v.ID})
		row.Command, row.Params = registry.Builtin+".card_delete", string(params)
	} else {
		master := registry.Actor{Kind: registry.Master, Workspace: p.WorkspaceID}
		var err error
		if out, err = a.Gateway.CallAs(ctx, master, s.Call.Command, s.Call.Input); err != nil {
			return nil, err
		}
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.AddUnattended(ctx, row) })
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("unattended")
	return map[string]any{"done": true, "result": out}, nil
}

func (a *App) suggestedCard(ctx context.Context, act registry.Actor, c *suggestedCard) (cardView, error) {
	out, err := a.cardCreate(ctx, act, struct {
		Project, Title, Text, Lane string
		Record                     int64
		Waits                      []int64
	}{Project: c.Project, Title: c.Title, Text: c.Text, Record: c.Record})
	if err != nil {
		return cardView{}, err
	}
	return out.(cardView), nil
}

// suggestionAnswer settles a suggestion: yes makes the card or the call, no rejects it with the reason, move
// gives a card suggestion to another project, where it waits again.
func (a *App) suggestionAnswer(ctx context.Context, act registry.Actor, q db.Ask, answer, project string) (any, error) {
	var s suggestion
	if err := json.Unmarshal([]byte(q.Payload), &s); err != nil {
		return nil, err
	}
	switch {
	case answer == "move":
		if s.Card == nil {
			return nil, registry.Invalid("deck: only a card suggestion moves to another project")
		}
		p, err := a.suggestTarget(ctx, act, strings.TrimSpace(project))
		if err != nil {
			return nil, err
		}
		if m, err := docs.Manifest(ctx, a.Store.Q, p); err != nil || m.RoleLane("inbox") == nil {
			return nil, registry.Invalid("deck: project %s has no inbox lane for a card", p.Name)
		}
		s.Card.Project = p.Name
		payload, _ := json.Marshal(s)
		var n int64
		err = a.Store.Tx(ctx, func(ctx context.Context, tx *db.Queries) error {
			var err error
			n, err = tx.MoveSuggestion(ctx, db.MoveSuggestionParams{ProjectID: p.ID, Text: s.Reason + "\n" + s.what(), Payload: string(payload), ID: q.ID})
			return err
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, notOpen
		}
		a.Bus.Publish("asks")
		return map[string]any{"suggestion": q.ID, "project": p.Name}, nil
	case answer == "allow":
		if err := a.Asks.Answer(ctx, q.ID, answer); err != nil {
			return nil, notOpen
		}
		if s.Card != nil {
			return a.suggestedCard(ctx, act, s.Card)
		}
		return a.Registry.Invoke(ctx, act, s.Call.Command, s.Call.Input)
	case answer == "deny" || strings.HasPrefix(answer, "deny:"):
		if err := a.Asks.Answer(ctx, q.ID, answer); err != nil {
			return nil, notOpen
		}
		return map[string]any{"suggestion": q.ID, "rejected": true}, nil
	}
	return nil, registry.Invalid("deck: answer a suggestion with allow, deny, deny:<reason> or move with project")
}

// suggestionsRejected are the suggestions the owner said no to lately, so a suggester does not make them again;
// an agent sees those of its own project and those it made.
func (a *App) suggestionsRejected(ctx context.Context, act registry.Actor, in struct{ Days int64 }) (any, error) {
	if in.Days <= 0 || in.Days > 365 {
		in.Days = 30
	}
	since := store.Time(a.Cfg.Now().Add(-time.Duration(in.Days) * 24 * time.Hour))
	rows, err := a.Store.Q.ListRejectedSuggestions(ctx, db.ListRejectedSuggestionsParams{WorkspaceID: act.Workspace, Since: sql.NullString{String: since, Valid: true}})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, q := range rows {
		var s suggestion
		if json.Unmarshal([]byte(q.Payload), &s) != nil {
			continue
		}
		if act.Kind == registry.Agent && q.ProjectID != act.ProjectID && !a.byProject(ctx, s.By, act.ProjectID) {
			continue
		}
		why := strings.TrimPrefix(strings.TrimPrefix(q.Answer, "deny"), ":")
		p, _ := a.Store.Q.GetProject(ctx, q.ProjectID)
		out = append(out, map[string]any{"project": p.Name, "what": s.what(), "reason": s.Reason, "why_not": untrusted.Cut(why, 500),
			"by": s.By, "answered_at": q.AnsweredAt.String})
	}
	return out, nil
}

// byProject reports whether an author like agent:<process> is a process of the project.
func (a *App) byProject(ctx context.Context, by string, projectID int64) bool {
	var id int64
	if _, err := fmt.Sscanf(by, "agent:%d", &id); err != nil {
		return false
	}
	p, err := a.Store.Q.GetProcess(ctx, id)
	return err == nil && p.ProjectID == projectID
}

// expireSuggestions closes suggestions the owner left unanswered past their term; that is not a refusal.
func (a *App) expireSuggestions(ctx context.Context, now time.Time) {
	n, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.ExpireSuggestions(ctx, store.Time(now))
	})
	if err != nil {
		slog.Error("expire suggestions", "err", err)
		return
	}
	if n > 0 {
		a.Bus.Publish("asks")
	}
}

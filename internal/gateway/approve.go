package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type approveInput struct {
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
}

type decision struct {
	Behavior     string         `json:"behavior"`
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Message      string         `json:"message,omitempty"`
}

func (g *Gateway) approve(ctx context.Context, s *Session, raw json.RawMessage) (*mcp.CallToolResult, error) {
	var in approveInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	var d decision
	if s.Question != "" && in.ToolName == s.Question {
		d = g.question(ctx, s, in)
	} else {
		d = g.confirm(ctx, s, in)
	}
	out, _ := json.Marshal(d)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
}

type question struct {
	Question string `json:"question"`
}

const stopAndWait = " Stop here and end your turn; deck will resume this session with the owner's decision."

func (g *Gateway) question(ctx context.Context, s *Session, in approveInput) decision {
	var qs struct {
		Questions []question `json:"questions"`
	}
	b, _ := json.Marshal(in.Input)
	_ = json.Unmarshal(b, &qs)
	texts := make([]string, 0, len(qs.Questions))
	for _, q := range qs.Questions {
		if strings.TrimSpace(q.Question) == "" {
			return decision{Behavior: "deny", Message: "deck: a question without text reaches no one: put the text of each question in its question field"}
		}
		texts = append(texts, q.Question)
	}
	if len(texts) == 0 {
		return decision{Behavior: "deny", Message: "deck: the call has no questions: put each in questions with its text in the question field"}
	}
	until := g.cfg.Now().Add(s.wait(g.cfg.QuestionWait))
	text := strings.Join(texts, "\n")
	open, _ := g.st.Q.ListOpenAsksOfProcess(ctx, s.ProcessID)
	a, err := g.again(open, text), error(nil)
	if a.ID == 0 {
		// A question waits as long as a confirmation; at its term the agent goes on without the answer.
		var expires time.Time
		if ttl := g.confirmTTL(ctx, s.Workspace); ttl > 0 {
			expires = g.cfg.Now().Add(ttl)
		}
		a, err = g.asks.Create(ctx, s.ProcessID, ask.KindQuestion, text, in.Input, expires)
	}
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not deliver the question: " + err.Error()}
	}
	answer, ok := g.awaitOwner(ctx, s, a.ID, until)
	if !ok {
		return decision{Behavior: "deny", Message: "deck: the owner has not answered yet." + stopAndWait}
	}
	g.delivered(ctx, a.ID)
	upd := map[string]any{}
	for k, v := range in.Input {
		upd[k] = v
	}
	upd["answers"] = QuestionAnswers(texts, answer)
	return decision{Behavior: "allow", UpdatedInput: upd}
}

// again is the open question of the process with the same text that no call waits on any more, which a question asked
// again waits on instead of a second one.
func (g *Gateway) again(open []db.Ask, text string) db.Ask {
	for _, o := range open {
		if o.Kind == ask.KindQuestion && o.State == ask.StateOpen && o.Text == text && g.asks.Rejoin(o.ID) {
			return o
		}
	}
	return db.Ask{}
}

// wait is how long a call of the session waits for the owner: d, or less when the provider times calls out.
func (s *Session) wait(d time.Duration) time.Duration {
	if s.Wait > 0 && s.Wait < d {
		return s.Wait
	}
	return d
}

// QuestionAnswers maps question texts to answers; a plain string answers every question.
func QuestionAnswers(texts []string, answer string) map[string]string {
	answers := map[string]string{}
	if json.Unmarshal([]byte(answer), &answers) == nil {
		return answers
	}
	for _, t := range texts {
		answers[t] = answer
	}
	return answers
}

// TrialRefusal is what an agent in a trial run gets for any call that needs the owner's confirmation.
const TrialRefusal = "deck: trial run: external actions are declined; this call was not made."

// confirm decides a call of a tool of the agent's provider as any call is decided: by the table, with the owner's
// answer waited for while the turn waits.
func (g *Gateway) confirm(ctx context.Context, s *Session, in approveInput) decision {
	act := s.actor()
	input, _ := json.Marshal(in.Input)
	params := Canonical(input)
	v := g.turnVerdict(ctx, act, in.ToolName, input)
	switch v.Does {
	case DoRefuse:
		g.log(ctx, s, in.ToolName, "", "rejected", params)
		if v.Name == "draft" {
			return decision{Behavior: "deny", Message: TrialRefusal}
		}
		return decision{Behavior: "deny", Message: "deck: " + v.Text}
	case DoAsk:
	default:
		if g.taken(ctx, act, in.ToolName, input, v) {
			g.log(ctx, s, in.ToolName, "", "done", params)
			return decision{Behavior: "allow", UpdatedInput: in.Input}
		}
	}
	a, err := g.turnRequest(ctx, act, in.ToolName, input)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not ask the owner: " + err.Error()}
	}
	answer, ok := g.awaitOwner(ctx, s, a.ID, g.cfg.Now().Add(s.wait(g.cfg.ConfirmWait)))
	if !ok {
		return decision{Behavior: "deny", Message: "deck: waiting for the owner to confirm this call; it was not made." + stopAndWait}
	}
	if v := g.turnVerdict(ctx, act, in.ToolName, input); v.Does != DoAsk && v.Does != DoRefuse && g.taken(ctx, act, in.ToolName, input, v) {
		g.delivered(ctx, a.ID)
		g.log(ctx, s, in.ToolName, "", "done", params)
		return decision{Behavior: "allow", UpdatedInput: in.Input}
	}
	g.delivered(ctx, a.ID)
	g.log(ctx, s, in.ToolName, "", "rejected", params)
	return decision{Behavior: "deny", Message: RejectMessage(answer)}
}

// turnVerdict decides a call of a tool of the agent's provider: deck's to answer, for the work of the turn or once.
func (g *Gateway) turnVerdict(ctx context.Context, act registry.Actor, tool string, input json.RawMessage) Verdict {
	v := Verdict{Facts: Facts{Needs: true, Declared: true, Draft: act.Trial}}
	allows, _ := g.allowsOf(ctx, act)
	same := Canonical(input)
	for _, a := range allows {
		switch {
		case a.Command != tool:
		case a.Any:
			v.Facts.Answer = Work
		case Canonical(a.Params) == same:
			v.Facts.Pressed = true
		}
	}
	v.Row = decide(v.Facts)
	return v
}

// taken takes the yes a row of a turn's call stands on: a call allowed once is taken; one for the work stays.
func (g *Gateway) taken(ctx context.Context, act registry.Actor, tool string, input json.RawMessage, v Verdict) bool {
	if v.Name != "pressed" {
		return true
	}
	took := false
	same := Canonical(input)
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, act.ProcessID)
		if err != nil {
			return err
		}
		var as []Allow
		_ = json.Unmarshal([]byte(p.Allows), &as)
		i := slices.IndexFunc(as, func(a Allow) bool { return a.Command == tool && !a.Any && Canonical(a.Params) == same })
		if i < 0 {
			return nil
		}
		took = true
		return q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: allowsText(slices.Delete(as, i, i+1)), ID: p.ID})
	})
	return took
}

// turnRequest is the request of a call of a tool of the agent's provider the turn waits on: the open one of the same
// call when an earlier call gave up waiting on it, else a new one.
func (g *Gateway) turnRequest(ctx context.Context, act registry.Actor, tool string, input json.RawMessage) (db.Ask, error) {
	f := Freeze(act, tool, input)
	f.As = AsTurn
	if act.ProcessID != 0 && !act.Trial {
		f.Offers = []string{Work}
	}
	same := Canonical(input)
	open, err := g.st.Q.ListOpenAsksOfProcess(ctx, act.ProcessID)
	if err != nil {
		return db.Ask{}, err
	}
	for _, o := range open {
		old, err := ask.Decode[Frozen](o.Payload)
		if err == nil && o.Kind == ask.KindCall && old.As == AsTurn && old.Call.Command == tool && Canonical(old.Call.Input) == same {
			if !g.asks.Rejoin(o.ID) {
				return db.Ask{}, fmt.Errorf("an identical call already waits for the owner (ask %d)", o.ID)
			}
			return o, nil
		}
	}
	var expires time.Time
	if ttl := g.confirmTTL(ctx, act.Workspace); ttl > 0 {
		expires = g.cfg.Now().Add(ttl)
	}
	text := fmt.Sprintf("Agent wants %s with %s", tool, untrusted.Cut(same, 500))
	a, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return Hold(ctx, q, act, act.ProjectID, act.ProcessID, ask.KindCall, "", text, f, expires, g.cfg.Now())
	})
	if err == nil && !g.asks.Rejoin(a.ID) {
		err = fmt.Errorf("ask %d has a waiter already", a.ID)
	}
	return a, err
}

// RejectMessage turns a "deny" or "deny:<reason>" answer into the refusal text the agent gets.
func RejectMessage(answer string) string {
	if answer == ask.Stale {
		return "deck: this call is out of date: what it acts on changed since it was asked for, so deck closed it unmade; ask again if it is still needed."
	}
	if by := ask.ReplacedBy(answer); by != 0 {
		return fmt.Sprintf("deck: your later call, ask %d, took the place of this one, so deck closed it unmade; only that one waits for the owner.", by)
	}
	msg := "deck: the owner rejected this call."
	if reason := strings.TrimPrefix(answer, "deny:"); reason != answer && reason != "" {
		msg += " Reason: " + untrusted.Line(reason, 500)
	}
	return msg
}

// awaitOwner waits until the owner answers ask id or until passes, with the ceiling of the process paused while any
// call of it waits for the owner; the ceiling runs again once none does, even when the call went away.
func (g *Gateway) awaitOwner(ctx context.Context, s *Session, id int64, until time.Time) (string, bool) {
	g.waits(ctx, s.ProcessID, 1)
	defer g.waits(context.WithoutCancel(ctx), s.ProcessID, -1)
	return g.asks.Wait(ctx, id, until)
}

// waits counts the calls of a process that wait for the owner: the first pauses its ceiling, the last runs it again.
func (g *Gateway) waits(ctx context.Context, processID int64, by int) {
	g.mu.Lock()
	n := g.owed[processID] + by
	if n <= 0 {
		delete(g.owed, processID)
	} else {
		g.owed[processID] = n
	}
	edge := by > 0 && n == 1 || by < 0 && n <= 0
	g.mu.Unlock()
	if !edge {
		return
	}
	now := g.cfg.Now().UnixMilli()
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if by < 0 {
			return q.StartCeiling(ctx, db.StartCeilingParams{Now: now, ID: processID})
		}
		return q.PauseCeiling(ctx, db.PauseCeilingParams{Now: now, ID: processID})
	})
}

func (g *Gateway) delivered(ctx context.Context, ask int64) {
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.DeliverAsk(ctx, db.DeliverAskParams{ID: ask, Now: store.Time(g.cfg.Now())})
		return err
	})
}

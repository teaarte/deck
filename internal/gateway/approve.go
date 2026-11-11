package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"deck2/internal/ask"
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
	for _, o := range open {
		if o.State == ask.StateOpen && o.Kind != ask.KindQuestion && names(text, o.ID) {
			return decision{Behavior: "deny", Message: fmt.Sprintf("deck: ask %d already waits for the owner, and deck brings you how they decide on it: "+
				"do not ask them about it. Go on with other work, or end your turn and deck resumes this session with the answer.", o.ID)}
		}
	}
	a, err := g.again(open, text), error(nil)
	if a.ID == 0 {
		a, err = g.asks.Create(ctx, s.ProcessID, ask.KindQuestion, text, in.Input, time.Time{})
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

// named finds an ask named in the text of a question: the word ask, request or запрос before its number, or its link.
var named = regexp.MustCompile(`(?i)(?:(?:^|[^\p{L}\p{N}_])(?:ask|request|confirmation|запрос\p{L}*|подтверждени\p{L}*)\s*[#№]?\s*|focus=)(\d+)`)

// names says the text of a question names ask id.
func names(text string, id int64) bool {
	for _, m := range named.FindAllStringSubmatch(text, -1) {
		if m[1] == strconv.FormatInt(id, 10) {
			return true
		}
	}
	return false
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

// ConfirmPayload is the frozen call a confirmation is about; an "allow" answer permits exactly this call once.
func ConfirmPayload(tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{"tool": tool, "input": input})
	return string(b)
}

// TrialRefusal is what an agent in a trial run gets for any call that needs the owner's confirmation.
const TrialRefusal = "deck: trial run: external actions are declined; this call was not made."

func (g *Gateway) confirm(ctx context.Context, s *Session, in approveInput) decision {
	payload := ConfirmPayload(in.ToolName, in.Input)
	if s.Trial {
		g.log(ctx, s, in.ToolName, "external", "rejected", payload)
		return decision{Behavior: "deny", Message: TrialRefusal}
	}
	consumed := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.ConsumeApproval(ctx, db.ConsumeApprovalParams{ProcessID: s.ProcessID, Payload: payload, Now: store.Time(g.cfg.Now())})
		return err
	})
	if consumed == nil {
		g.log(ctx, s, in.ToolName, "external", "done", payload)
		return decision{Behavior: "allow", UpdatedInput: in.Input}
	}
	g.log(ctx, s, in.ToolName, "external", "waiting", payload)
	params, _ := json.Marshal(in.Input)
	text := fmt.Sprintf("Agent wants %s with %s", in.ToolName, untrusted.Cut(string(params), 500))
	now := g.cfg.Now()
	var expires time.Time
	if ttl := g.confirmTTL(ctx, s.Workspace); ttl > 0 {
		expires = now.Add(ttl)
	}
	a, err := g.asks.CreateRaw(ctx, s.ProcessID, ask.KindConfirm, text, payload, expires)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not request confirmation: " + err.Error()}
	}
	answer, ok := g.awaitOwner(ctx, s, a.ID, now.Add(s.wait(g.cfg.ConfirmWait)))
	if !ok {
		return decision{Behavior: "deny", Message: "deck: waiting for the owner to confirm this call; it was not made." + stopAndWait}
	}
	if answer == "allow" {
		n, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
			return q.UseApproval(ctx, db.UseApprovalParams{ID: a.ID, Now: store.Time(g.cfg.Now())})
		})
		if err != nil || n == 0 {
			return decision{Behavior: "deny", Message: "deck: this approval was already used by an identical call."}
		}
		g.log(ctx, s, in.ToolName, "external", "done", "")
		return decision{Behavior: "allow", UpdatedInput: in.Input}
	}
	g.delivered(ctx, a.ID)
	g.log(ctx, s, in.ToolName, "external", "rejected", "")
	return decision{Behavior: "deny", Message: RejectMessage(answer)}
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

// awaitOwner waits until the owner answers ask id or until passes, with the ceiling of the process paused
// meanwhile; the ceiling runs again even when the call went away.
func (g *Gateway) awaitOwner(ctx context.Context, s *Session, id int64, until time.Time) (string, bool) {
	g.ceiling(ctx, s.ProcessID, false)
	defer g.ceiling(context.WithoutCancel(ctx), s.ProcessID, true)
	return g.asks.Wait(ctx, id, until)
}

// ceiling runs or pauses the ceiling of a process: it does not tick while the owner owes a blocking answer.
func (g *Gateway) ceiling(ctx context.Context, processID int64, run bool) {
	now := g.cfg.Now().UnixMilli()
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if run {
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

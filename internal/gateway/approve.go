package gateway

import (
	"context"
	"deck2/internal/ask"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	if in.ToolName == QuestionTool {
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
		texts = append(texts, q.Question)
	}
	until := g.cfg.Now().Add(g.cfg.QuestionWait)
	a, err := g.asks.Create(ctx, s.ProcessID, ask.KindQuestion, strings.Join(texts, "\n"), in.Input, until)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not deliver the question: " + err.Error()}
	}
	g.ceiling(ctx, s.ProcessID, false)
	answer, ok := g.asks.Wait(ctx, a.ID, until)
	g.ceiling(ctx, s.ProcessID, true)
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
		_, err := q.ConsumeApproval(ctx, db.ConsumeApprovalParams{ProcessID: s.ProcessID, Payload: payload})
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
	if g.cfg.ConfirmTTL > 0 {
		expires = now.Add(g.cfg.ConfirmTTL)
	}
	a, err := g.asks.CreateRaw(ctx, s.ProcessID, ask.KindConfirm, text, payload, expires)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not request confirmation: " + err.Error()}
	}
	answer, ok := g.asks.Wait(ctx, a.ID, now.Add(g.cfg.ConfirmWait))
	if !ok {
		return decision{Behavior: "deny", Message: "deck: waiting for the owner to confirm this call; it was not made." + stopAndWait}
	}
	if answer == "allow" {
		var n int64
		err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			var err error
			n, err = q.UseApproval(ctx, a.ID)
			return err
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
	msg := "deck: the owner rejected this call."
	if reason := strings.TrimPrefix(answer, "deny:"); reason != answer && reason != "" {
		msg += " Reason: " + untrusted.Line(reason, 500)
	}
	return msg
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
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.MarkAskDelivered(ctx, ask) })
}

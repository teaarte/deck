package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"deck2/internal/provider"
)

type line struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	ParentToolUseID  string          `json:"parent_tool_use_id"`
	TaskID           string          `json:"task_id"`
	Result           string          `json:"result"`
	IsError          bool            `json:"is_error"`
	Errors           []string        `json:"errors"`
	Error            string          `json:"error"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Denials          []struct {
		ToolName string `json:"tool_name"`
	} `json:"permission_denials"`
	Model        string   `json:"model"`
	DurationMs   int64    `json:"duration_ms"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	Usage        *struct {
		Input         int64 `json:"input_tokens"`
		Output        int64 `json:"output_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Message *struct {
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	RateLimitInfo *struct {
		Status   string `json:"status"`
		ResetsAt int64  `json:"resetsAt"`
	} `json:"rate_limit_info"`
}

// turn collects what one CLI invocation reported.
type turn struct {
	req        provider.Request
	res        provider.Result
	results    int
	foreign    string
	authFailed bool
	model      string
	// live counts the owner's messages written into the turn; one written as the turn ended starts another.
	live int
	// background counts background tasks that finished; each wakes the session for one more result.
	background int
	// tasks are the tasks of the session that started and have not reported yet.
	tasks map[string]bool
	// started is set once the CLI reported its session; a message before that could meet a session that is gone.
	started bool
	// cost is the cost of the session the results so far reported.
	cost float64
}

func (t *turn) line(raw []byte) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	if l.SessionID != "" && l.SessionID != t.req.SessionID && t.foreign == "" {
		t.foreign = l.SessionID
	}
	if l.Type == "system" && l.Subtype == "task_notification" && l.ParentToolUseID == "" {
		t.background++
		delete(t.tasks, l.TaskID)
	}
	if l.Type == "system" && l.Subtype == "task_started" && l.ParentToolUseID == "" && l.TaskID != "" {
		if t.tasks == nil {
			t.tasks = map[string]bool{}
		}
		t.tasks[l.TaskID] = true
	}
	if l.ParentToolUseID != "" || (l.Type == "system" && strings.HasPrefix(l.Subtype, "task_")) {
		return
	}
	emit := t.req.Emit
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			t.model, t.started = l.Model, true
		}
	case "assistant":
		if l.Error == "authentication_failed" {
			t.authFailed = true
		}
		if l.Error == "" {
			t.res.RateLimited = false
		}
		if l.Message == nil {
			return
		}
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				emit(provider.Event{Kind: provider.EventMessage, Text: c.Text})
			case "thinking":
				emit(provider.Event{Kind: provider.EventThought, Text: c.Thinking})
			case "tool_use":
				emit(provider.Event{Kind: provider.EventTool, ID: c.ID, Title: c.Name, Text: string(c.Input)})
			}
		}
	case "user":
		if l.Message == nil {
			return
		}
		for _, c := range l.Message.Content {
			if c.Type == "tool_result" {
				emit(provider.Event{Kind: provider.EventToolUpdate, ID: c.ToolUseID, Status: provider.ToolStatus(!c.IsError), Text: resultText(c.Content)})
			}
		}
	case "rate_limit_event":
		if l.RateLimitInfo == nil {
			return
		}
		if l.RateLimitInfo.Status == "rejected" {
			t.res.RateLimited = true
			t.res.ResetsAt = l.RateLimitInfo.ResetsAt
		}
		emit(provider.Event{Kind: provider.EventRateLimit, Text: l.RateLimitInfo.Status})
	case "result":
		t.results++
		if t.results > t.allowed() {
			return
		}
		if l.SessionID == "" && t.foreign == "" {
			t.foreign = "without an id"
		}
		t.usage(l)
		t.res.Text = l.Result
		if t.res.Text == "" {
			t.res.Text = strings.Join(l.Errors, "; ")
		}
		t.res.IsError = l.IsError || l.Subtype != "success"
		for _, d := range l.Denials {
			t.res.Denied = append(t.res.Denied, d.ToolName)
		}
		var out struct {
			Result json.RawMessage `json:"result"`
		}
		t.res.Structured = nil
		if len(t.req.ResultSchema) > 0 && json.Unmarshal(l.StructuredOutput, &out) == nil && out.Result != nil {
			t.res.Structured = out.Result
		}
		for _, e := range l.Errors {
			if t.req.Resume && l.Subtype == "error_during_execution" && strings.Contains(e, "No conversation found") {
				t.res.SessionMissing = true
			}
		}
	}
}

// usage reports the token counts of the turn from its result. modelUsage and the cost there add up the whole
// session, so the counts come from usage and the cost only from a turn that started the session.
func (t *turn) usage(l line) {
	if t.req.OnUsage == nil || l.Usage == nil {
		return
	}
	u := provider.Usage{Model: t.model, Input: l.Usage.Input, Output: l.Usage.Output, CacheRead: l.Usage.CacheRead,
		CacheCreation: l.Usage.CacheCreation, Duration: time.Duration(l.DurationMs) * time.Millisecond}
	if !t.req.Resume && l.TotalCostUSD != nil {
		cost := *l.TotalCostUSD - t.cost
		t.cost = *l.TotalCostUSD
		u.CostUSD = &cost
	}
	t.req.OnUsage(u)
}

// finish accepts the turn only if it produced exactly one result for the session deck assigned and the
// CLI read the whole prompt.
func (t *turn) finish(waitErr error, stderr string, promptErr error) (provider.Result, error) {
	res := t.res
	switch {
	case res.SessionMissing:
		return res, nil
	case t.results == 0:
		return res, fmt.Errorf("claude exited without a result: %v: %s", waitErr, stderr)
	case t.results > t.allowed():
		return res, fmt.Errorf("claude reported %d results in one turn", t.results)
	case t.foreign != "":
		return res, fmt.Errorf("claude ran session %s instead of %s assigned by deck", t.foreign, t.req.SessionID)
	case promptErr != nil:
		return res, fmt.Errorf("the prompt did not reach claude in full: %v", promptErr)
	}
	res.NeedsLogin = res.IsError && t.authFailed
	return res, nil
}

// resultText is the text of a tool result: a string, or the text parts of a list of blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		if p.Type == "text" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// allowed is how many results the turn may report: one, and one more per message written into it and per
// background task that woke it.
func (t *turn) allowed() int { return 1 + t.live + t.background }

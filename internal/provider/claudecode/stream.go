package claudecode

import (
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"strings"
)

type line struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	ParentToolUseID  string          `json:"parent_tool_use_id"`
	Result           string          `json:"result"`
	IsError          bool            `json:"is_error"`
	Errors           []string        `json:"errors"`
	Error            string          `json:"error"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Message          *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
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
}

func (t *turn) line(raw []byte) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	if l.SessionID != "" && l.SessionID != t.req.SessionID && t.foreign == "" {
		t.foreign = l.SessionID
	}
	if l.ParentToolUseID != "" || (l.Type == "system" && strings.HasPrefix(l.Subtype, "task_")) {
		return
	}
	emit := func(e provider.Event) {
		if t.req.OnEvent != nil {
			t.req.OnEvent(e)
		}
	}
	switch l.Type {
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
				emit(provider.Event{Kind: "text", Text: c.Text})
			case "tool_use":
				emit(provider.Event{Kind: "tool", Text: c.Name})
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
		emit(provider.Event{Kind: "rate_limit", Raw: append(json.RawMessage{}, raw...)})
	case "result":
		t.results++
		if t.results > 1 {
			return
		}
		if l.SessionID == "" && t.foreign == "" {
			t.foreign = "without an id"
		}
		t.res.Text = l.Result
		if t.res.Text == "" {
			t.res.Text = strings.Join(l.Errors, "; ")
		}
		t.res.IsError = l.IsError || l.Subtype != "success"
		var out struct {
			Result json.RawMessage `json:"result"`
		}
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

// finish accepts the turn only if it produced exactly one result for the session deck assigned and the
// CLI read the whole prompt.
func (t *turn) finish(waitErr error, stderr string, promptErr error) (provider.Result, error) {
	res := t.res
	res.SessionID = t.req.SessionID
	switch {
	case res.SessionMissing:
		return res, nil
	case t.results == 0:
		return res, fmt.Errorf("claude exited without a result: %v: %s", waitErr, stderr)
	case t.results > 1:
		return res, fmt.Errorf("claude reported %d results in one turn", t.results)
	case t.foreign != "":
		return res, fmt.Errorf("claude ran session %s instead of %s assigned by deck", t.foreign, t.req.SessionID)
	case promptErr != nil:
		return res, fmt.Errorf("the prompt did not reach claude in full: %v", promptErr)
	}
	res.NeedsLogin = res.IsError && t.authFailed
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/provider"
)

// finish accepts the turn only if it completed exactly once, without an error, in the thread of the session.
func (t *turn) finish() (provider.Result, error) {
	res := t.res
	if t.completed > 1 {
		return res, fmt.Errorf("codex reported %d completions of one turn", t.completed)
	}
	res.Text = t.final
	if t.limitHit != "" && t.last.Status != "completed" {
		res.RateLimited, res.ResetsAt, res.Text = true, t.resetsAt(), "codex: usage limit reached: "+t.limitHit
		return res, nil
	}
	switch t.last.Status {
	case "completed":
		return res, nil
	case "interrupted":
		res.IsError, res.Text = true, "codex: the turn was interrupted"
		return res, nil
	}
	res.IsError = true
	e := t.failure
	if t.last.Error != nil {
		e = t.last.Error
	}
	if e == nil {
		res.Text = "codex: the turn ended as " + string(t.last.Status)
		return res, nil
	}
	res.Text = e.Message
	var info string
	_ = json.Unmarshal(e.CodexErrorInfo, &info)
	switch {
	case info == "usageLimitExceeded":
		res.RateLimited, res.ResetsAt = true, t.resetsAt()
	case info == "unauthorized" || strings.Contains(string(e.CodexErrorInfo), `"httpStatusCode":401`) || strings.Contains(e.Message, "401 Unauthorized"):
		res.NeedsLogin = true
	case unavailable(info, e.CodexErrorInfo):
		res.Unavailable = true
	}
	return res, nil
}

// resetsAt is when the window that ran out resets, in unix seconds; zero when no window says.
func (t *turn) resetsAt() int64 {
	var at int64
	for _, w := range []*RateLimitWindow{t.limits.Primary, t.limits.Secondary} {
		if w != nil && w.UsedPercent >= 100 && w.ResetsAt > at {
			at = w.ResetsAt
		}
	}
	return at
}

// unavailable reports an error of codex that says its model did not answer rather than that the task failed.
func unavailable(info string, raw json.RawMessage) bool {
	if info == "serverOverloaded" || info == "internalServerError" {
		return true
	}
	var v map[string]struct {
		HTTPStatusCode int64 `json:"httpStatusCode"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	for k, d := range v {
		switch k {
		case "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts", "httpConnectionFailed":
			return d.HTTPStatusCode == 0 || d.HTTPStatusCode >= 500 || d.HTTPStatusCode == 429
		}
	}
	return false
}

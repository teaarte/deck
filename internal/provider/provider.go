package provider

import (
	"context"
	"encoding/json"
)

type Request struct {
	Cwd string
	// Repo means Cwd is a code repository: its own instructions load, those of the folders above do not.
	Repo          bool
	Prompt        string
	ResultSchema  json.RawMessage
	MCPURL        string
	AllowedTools  []string
	PermissionMCP string
	// SessionID is assigned by deck; Resume continues it instead of starting it.
	SessionID string
	Resume    bool
	OnEvent   func(Event)
	// OnStart gets the id of the process group the provider runs in, so a later deck can kill it.
	OnStart func(pgid int)
}

// Event is an event of the process itself; events of the provider's sub-agents are not reported.
type Event struct {
	Kind string // "text", "tool", "rate_limit"
	Text string
	Raw  json.RawMessage
}

type Result struct {
	SessionID string
	// Structured is the result by the schema; JSON null means there was nothing to do.
	Structured json.RawMessage
	Text       string
	IsError    bool
	// NeedsLogin is set when the turn failed because the provider's login is missing, expired or refused.
	NeedsLogin bool
	// SessionMissing is set when a resume found no stored session, so the session has to start over.
	SessionMissing bool
	// RateLimited is set when the provider refused on a subscription limit; ResetsAt is unix seconds.
	RateLimited bool
	ResetsAt    int64
}

type Provider interface {
	Name() string
	// QuestionTool names the provider tool that asks the owner a blocking question, or is empty.
	QuestionTool() string
	Run(ctx context.Context, req Request) (Result, error)
}

// Reaper kills a process group an earlier deck left running, if it still runs the given session.
type Reaper interface {
	Reap(pgid int, session string)
}

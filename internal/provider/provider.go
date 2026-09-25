package provider

import (
	"context"
	"encoding/json"
)

type Request struct {
	Cwd           string
	Prompt        string
	ResultSchema  json.RawMessage
	MCPURL        string
	AllowedTools  []string
	PermissionMCP string
	ResumeSession string
	OnSession     func(id string)
	OnEvent       func(Event)
}

type Event struct {
	Kind string // "text", "tool", "rate_limit"
	Text string
	Raw  json.RawMessage
}

type Result struct {
	SessionID  string
	Structured json.RawMessage
	Text       string
	IsError    bool
	// RateLimited is set when the provider stopped on a subscription limit; ResetsAt is unix seconds.
	RateLimited bool
	ResetsAt    int64
}

type Provider interface {
	Name() string
	Run(ctx context.Context, req Request) (Result, error)
}

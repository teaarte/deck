package acp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/provider"
)

// client is deck's side of one session: it keeps what the agent says and lets through what the agent asks
// about, since the sandbox of deck bounds the agent and the gateway judges its external calls.
type client struct {
	mu      sync.Mutex
	session sdk.SessionId
	text    strings.Builder
	thought strings.Builder
	all     strings.Builder
	onEvent func(provider.Event)
	// input marks tool calls whose arguments were reported.
	input map[sdk.ToolCallId]bool
}

func (c *client) emit(e provider.Event) {
	if c.onEvent != nil {
		c.onEvent(e)
	}
}

// flush reports the message and the thought the agent has streamed since the last tool call.
func (c *client) flush() {
	c.mu.Lock()
	t, th := c.text.String(), c.thought.String()
	c.text.Reset()
	c.thought.Reset()
	c.mu.Unlock()
	if th = strings.TrimSpace(th); th != "" {
		c.emit(provider.Event{Kind: provider.EventThought, Text: th})
	}
	if t = strings.TrimSpace(t); t != "" {
		c.emit(provider.Event{Kind: provider.EventMessage, Text: t})
	}
}

// said is all the agent said in the turn.
func (c *client) said() string {
	c.flush()
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.all.String())
}

func (c *client) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	c.mu.Lock()
	mine := c.session != "" && n.SessionId == c.session
	c.mu.Unlock()
	if !mine {
		return nil
	}
	switch u := n.Update; {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		c.mu.Lock()
		if c.thought.Len() > 0 {
			c.mu.Unlock()
			c.flush()
			c.mu.Lock()
		}
		c.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
		c.all.WriteString(u.AgentMessageChunk.Content.Text.Text)
		c.mu.Unlock()
	case u.AgentThoughtChunk != nil && u.AgentThoughtChunk.Content.Text != nil:
		c.mu.Lock()
		if c.text.Len() > 0 {
			c.mu.Unlock()
			c.flush()
			c.mu.Lock()
		}
		c.thought.WriteString(u.AgentThoughtChunk.Content.Text.Text)
		c.mu.Unlock()
	case u.ToolCall != nil:
		c.flush()
		in := raw(u.ToolCall.RawInput)
		c.mu.Lock()
		if c.input == nil {
			c.input = map[sdk.ToolCallId]bool{}
		}
		c.input[u.ToolCall.ToolCallId] = in != ""
		c.mu.Unlock()
		c.emit(provider.Event{Kind: provider.EventTool, ID: string(u.ToolCall.ToolCallId), Title: u.ToolCall.Title, Text: in})
	case u.ToolCallUpdate != nil:
		up := u.ToolCallUpdate
		if up.Status == nil || (*up.Status != sdk.ToolCallStatusCompleted && *up.Status != sdk.ToolCallStatusFailed) {
			return nil
		}
		c.mu.Lock()
		known := c.input[up.ToolCallId]
		c.mu.Unlock()
		if in := raw(up.RawInput); !known && in != "" {
			title := ""
			if up.Title != nil {
				title = *up.Title
			}
			c.emit(provider.Event{Kind: provider.EventTool, ID: string(up.ToolCallId), Title: title, Text: in})
		}
		out := raw(up.RawOutput)
		if out == "" {
			var parts []string
			for _, ct := range up.Content {
				if ct.Content != nil && ct.Content.Content.Text != nil {
					parts = append(parts, ct.Content.Content.Text.Text)
				}
			}
			out = strings.Join(parts, "\n")
		}
		c.emit(provider.Event{Kind: provider.EventToolUpdate, ID: string(up.ToolCallId),
			Status: provider.ToolStatus(*up.Status == sdk.ToolCallStatusCompleted), Text: out})
	}
	return nil
}

// raw is a raw value of the protocol as text: a string as is, anything else as JSON.
func raw(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, _ := json.Marshal(v)
	if string(b) == "{}" || string(b) == "null" {
		return ""
	}
	return string(b)
}

// RequestPermission allows the one call asked about; "always" is the owner's button, never deck's.
func (c *client) RequestPermission(_ context.Context, r sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	for _, o := range r.Options {
		if o.Kind == sdk.PermissionOptionKindAllowOnce {
			return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected(o.OptionId)}, nil
		}
	}
	for _, o := range r.Options {
		if o.Kind == sdk.PermissionOptionKindRejectOnce {
			return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected(o.OptionId)}, nil
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

func (c *client) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, sdk.NewMethodNotFound("fs/read_text_file")
}

func (c *client) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, sdk.NewMethodNotFound("fs/write_text_file")
}

func (c *client) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, sdk.NewMethodNotFound("terminal/create")
}

func (c *client) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, sdk.NewMethodNotFound("terminal/kill")
}

func (c *client) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, sdk.NewMethodNotFound("terminal/output")
}

func (c *client) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, sdk.NewMethodNotFound("terminal/release")
}

func (c *client) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, sdk.NewMethodNotFound("terminal/wait_for_exit")
}

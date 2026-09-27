package acp

import (
	"context"
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
	all     strings.Builder
	onEvent func(provider.Event)
	asked   int
}

func (c *client) emit(kind, text string) {
	if c.onEvent != nil && text != "" {
		c.onEvent(provider.Event{Kind: kind, Text: text})
	}
}

// flush reports the message the agent has said since the last tool call.
func (c *client) flush() {
	c.mu.Lock()
	t := c.text.String()
	c.text.Reset()
	c.mu.Unlock()
	c.emit("text", strings.TrimSpace(t))
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
		c.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
		c.all.WriteString(u.AgentMessageChunk.Content.Text.Text)
		c.mu.Unlock()
	case u.ToolCall != nil:
		c.flush()
		c.emit("tool", u.ToolCall.Title)
	}
	return nil
}

// RequestPermission allows the one call asked about; "always" is the owner's button, never deck's.
func (c *client) RequestPermission(_ context.Context, r sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.asked++
	c.mu.Unlock()
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

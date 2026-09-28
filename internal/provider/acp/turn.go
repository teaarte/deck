package acp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

// turn is one ACP session of a deck turn: it opens or resumes the session and runs one prompt.
type turn struct {
	a       *Adapter
	req     provider.Request
	c       *client
	conn    *sdk.ClientSideConnection
	dir     string
	session string
	mcp     string
	e       *entry
}

func (t *turn) run(ctx context.Context) (provider.Result, error) {
	life := context.WithoutCancel(ctx)
	init, err := t.conn.Initialize(ctx, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber,
		ClientInfo: &sdk.Implementation{Name: "deck", Version: "0"}})
	if err != nil {
		return provider.Result{}, fmt.Errorf("acp initialize: %w", err)
	}
	caps := init.AgentCapabilities
	if !caps.McpCapabilities.Http || caps.SessionCapabilities.Resume == nil {
		return provider.Result{}, fmt.Errorf("acp: %s lacks HTTP MCP servers or session/resume, which deck needs", t.a.cfg.Agent)
	}
	servers := []sdk.McpServer{{Http: &sdk.McpServerHttpInline{Type: "http", Name: provider.Server, Url: t.mcp, Headers: []sdk.HttpHeader{}}}}
	if t.session == "" {
		r, err := t.conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: t.req.Cwd, McpServers: servers})
		if err != nil {
			return provider.Result{}, fmt.Errorf("acp session/new: %w", err)
		}
		if r.SessionId == "" {
			return provider.Result{}, errors.New("acp: the agent opened a session without an id")
		}
		t.session = string(r.SessionId)
		if err := provider.WriteFile(filepath.Join(t.dir, "session"), []byte(t.session)); err != nil {
			return provider.Result{}, err
		}
	} else if _, err := t.conn.ResumeSession(ctx, sdk.ResumeSessionRequest{SessionId: sdk.SessionId(t.session), Cwd: t.req.Cwd, McpServers: servers}); err != nil {
		if ctx.Err() == nil && !t.listed(ctx) {
			return provider.Result{SessionMissing: true}, nil
		}
		return provider.Result{}, fmt.Errorf("acp session/resume: %w", err)
	}
	t.c.mu.Lock()
	t.c.session = sdk.SessionId(t.session)
	t.c.mu.Unlock()
	type answer struct {
		r   sdk.PromptResponse
		err error
	}
	done := make(chan answer, 1)
	go func() {
		r, err := t.conn.Prompt(life, sdk.PromptRequest{SessionId: sdk.SessionId(t.session), Prompt: []sdk.ContentBlock{sdk.TextBlock(t.req.Prompt)}})
		done <- answer{r, err}
	}()
	var got answer
	select {
	case got = <-done:
	case <-ctx.Done():
		_ = t.conn.Cancel(life, sdk.CancelNotification{SessionId: sdk.SessionId(t.session)})
		select {
		case got = <-done:
		case <-time.After(15 * time.Second):
			return provider.Result{}, ctx.Err()
		}
	case <-t.conn.Done():
		return provider.Result{}, errors.New("acp: the agent exited before finishing the turn")
	}
	if got.err != nil {
		return t.e.failed(got.err), nil
	}
	res := provider.Result{Text: t.c.said()}
	switch got.r.StopReason {
	case sdk.StopReasonEndTurn:
	case sdk.StopReasonCancelled:
		res.IsError, res.Text = true, "acp: the turn was interrupted"
	default:
		res = t.e.refused(provider.Result{IsError: true, Text: "acp: the turn ended as " + string(got.r.StopReason)})
	}
	return res, nil
}

// listed reports whether the agent still has the session; an agent without session/list is taken to have it.
func (t *turn) listed(ctx context.Context) bool {
	var cursor *string
	for {
		r, err := t.conn.ListSessions(ctx, sdk.ListSessionsRequest{Cursor: cursor})
		if err != nil {
			return true
		}
		for _, s := range r.Sessions {
			if string(s.SessionId) == t.session {
				return true
			}
		}
		if r.NextCursor == nil || *r.NextCursor == "" {
			return false
		}
		cursor = r.NextCursor
	}
}

// failed is a prompt the agent answered with an error.
func (e *entry) failed(err error) provider.Result {
	text := err.Error()
	var re *sdk.RequestError
	if errors.As(err, &re) && re.Data != nil {
		text = fmt.Sprintf("%s: %v", re.Message, re.Data)
	}
	return e.refused(provider.Result{IsError: true, Text: "acp: " + untrusted.Cut(text, 2000)})
}

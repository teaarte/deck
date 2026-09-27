package codex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serve answers a request of the app-server. What Codex would ask a person goes to deck as a confirmation of
// the call; "yes" allows exactly that call, and the profile still bounds it.
func (t *turn) serve(ctx context.Context, m message) {
	switch m.Method {
	case "item/commandExecution/requestApproval":
		var p CommandExecutionRequestApprovalParams
		_ = json.Unmarshal(m.Params, &p)
		if t.req.NoNetwork && (p.NetworkApprovalContext != nil || p.AdditionalPermissions != nil && p.AdditionalPermissions.Network != nil) {
			t.refuse(m.ID, p.ThreadID, p.TurnID, "deck: this project has no network; the call was not made.")
			return
		}
		input := map[string]any{"command": p.Command, "cwd": string(p.Cwd)}
		if p.Kind != "" && p.Kind != "command" {
			input["kind"] = p.Kind
		}
		if p.AdditionalPermissions != nil {
			input["permissions"] = p.AdditionalPermissions
		}
		if p.NetworkApprovalContext != nil {
			input["network"] = p.NetworkApprovalContext
		}
		t.decide(ctx, m.ID, p.ThreadID, p.TurnID, p.ItemID, "shell", input)
	case "item/fileChange/requestApproval":
		var p FileChangeRequestApprovalParams
		_ = json.Unmarshal(m.Params, &p)
		t.decide(ctx, m.ID, p.ThreadID, p.TurnID, p.ItemID, "apply_patch", map[string]any{"changes": t.changes(p.ItemID), "grant_root": p.GrantRoot})
	case "mcpServer/elicitation/request":
		_ = t.c.reply(m.ID, MCPServerElicitationRequestResponse{Action: "decline"}, "")
	case "item/tool/requestUserInput":
		_ = t.c.reply(m.ID, nil, "deck: ask the owner with the ask tool of the deck MCP server")
	default:
		_ = t.c.reply(m.ID, nil, "deck does not handle "+m.Method)
	}
}

// changes are the file changes of an item, as its start announced them.
func (t *turn) changes(item string) []FileUpdateChange {
	t.gwMu.Lock()
	defer t.gwMu.Unlock()
	return t.patches[item]
}

func (t *turn) ours(thread, turnID string) bool {
	t.gwMu.Lock()
	defer t.gwMu.Unlock()
	return thread == t.started && turnID == t.startedTurn
}

// decide asks deck about one call Codex wants approved and steers the refusal text into the turn.
func (t *turn) decide(ctx context.Context, id json.RawMessage, thread, turnID, item, tool string, input map[string]any) {
	if !t.ours(thread, turnID) {
		_ = t.c.reply(id, map[string]string{"decision": "decline"}, "")
		return
	}
	allow, msg := t.approve(ctx, tool, input, item)
	if allow {
		_ = t.c.reply(id, map[string]string{"decision": "accept"}, "")
		return
	}
	t.refuse(id, thread, turnID, msg)
}

// refuse declines a request and tells the model why in the same turn.
func (t *turn) refuse(id json.RawMessage, thread, turnID, msg string) {
	_ = t.c.reply(id, map[string]string{"decision": "decline"}, "")
	if msg != "" && t.ours(thread, turnID) {
		text, _ := json.Marshal(TextUserInput{Type: "text", Text: msg})
		_, _ = t.c.start("turn/steer", TurnSteerParams{ThreadID: thread, ExpectedTurnID: turnID, Input: []UserInput{text}})
	}
}

// approve calls the permission tool of the gateway the way Claude Code does.
func (t *turn) approve(ctx context.Context, tool string, input map[string]any, item string) (bool, string) {
	gw, err := t.gateway(ctx)
	if err != nil {
		return false, "deck: could not reach deck to ask the owner: " + err.Error()
	}
	res, err := gw.CallTool(ctx, &mcp.CallToolParams{Name: approveTool(t.req),
		Arguments: map[string]any{"tool_name": tool, "input": input, "tool_use_id": item}})
	if err != nil {
		return false, "deck: " + err.Error()
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	var d struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if res.IsError || json.Unmarshal([]byte(b.String()), &d) != nil {
		return false, "deck: " + b.String()
	}
	return d.Behavior == "allow", d.Message
}

func (t *turn) gateway(ctx context.Context) (*mcp.ClientSession, error) {
	t.gwMu.Lock()
	defer t.gwMu.Unlock()
	if t.gw != nil {
		return t.gw, nil
	}
	s, err := gateway(context.WithoutCancel(ctx), t.req.MCPURL)
	if err != nil {
		return nil, err
	}
	t.gw = s
	return s, nil
}

func (t *turn) closeGateway() {
	t.gwMu.Lock()
	defer t.gwMu.Unlock()
	if t.gw != nil {
		_ = t.gw.Close()
	}
}

package codex

import (
	"encoding/json"
	"strings"

	"deck2/internal/provider"
)

// toolStarted reports a tool call as the item begins.
func (t *turn) toolStarted(raw ThreadItem) {
	var head struct{ Type, ID string }
	_ = json.Unmarshal(raw, &head)
	tool := func(title, text string) {
		t.emit(provider.Event{Kind: provider.EventTool, ID: head.ID, Title: title, Text: text})
	}
	switch head.Type {
	case "commandExecution":
		var it CommandExecutionThreadItem
		if decode(raw, &it) {
			tool("shell", it.Command)
		}
	case "mcpToolCall":
		var it MCPToolCallThreadItem
		if decode(raw, &it) {
			tool("mcp__"+it.Server+"__"+it.Tool, string(it.Arguments))
		}
	case "webSearch":
		var it WebSearchThreadItem
		if decode(raw, &it) {
			tool("web_search", it.Query)
		}
	case "dynamicToolCall", "collabAgentToolCall", "imageGeneration", "imageView":
		var it struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
			Prompt    string          `json:"prompt"`
			Path      string          `json:"path"`
		}
		_ = json.Unmarshal(raw, &it)
		title, text := it.Tool, it.Prompt+it.Path
		if title == "" {
			title = head.Type
		}
		if len(it.Arguments) > 0 && string(it.Arguments) != "null" {
			text = string(it.Arguments)
		}
		tool(title, text)
	case "fileChange":
		var it FileChangeThreadItem
		if decode(raw, &it) {
			paths := make([]string, 0, len(it.Changes))
			for _, c := range it.Changes {
				paths = append(paths, c.Path)
			}
			tool("apply_patch", strings.Join(paths, "\n"))
		}
	}
}

func (t *turn) item(raw ThreadItem) {
	var head struct{ Type, ID string }
	_ = json.Unmarshal(raw, &head)
	done := func(ok bool, text string) {
		t.emit(provider.Event{Kind: provider.EventToolUpdate, ID: head.ID, Status: provider.ToolStatus(ok), Text: text})
	}
	switch head.Type {
	case "agentMessage":
		var it AgentMessageThreadItem
		if decode(raw, &it) {
			t.emit(provider.Event{Kind: provider.EventMessage, Text: it.Text})
			if string(it.Phase) == "final_answer" {
				t.final = it.Text
			}
		}
	case "reasoning":
		var it ReasoningThreadItem
		if decode(raw, &it) && len(it.Summary)+len(it.Content) > 0 {
			text := strings.Join(it.Summary, "\n")
			if text == "" {
				text = strings.Join(it.Content, "\n")
			}
			t.emit(provider.Event{Kind: provider.EventThought, Text: text})
		}
	case "commandExecution":
		var it CommandExecutionThreadItem
		if decode(raw, &it) {
			done(it.Status == "completed", it.AggregatedOutput)
			if it.Status == "declined" {
				t.res.Denied = append(t.res.Denied, "shell")
			}
		}
	case "mcpToolCall":
		var it MCPToolCallThreadItem
		if decode(raw, &it) {
			text := ""
			if it.Result != nil {
				b, _ := json.Marshal(it.Result)
				text = string(b)
			}
			done(it.Status == "completed", text)
		}
	case "webSearch":
		done(true, "")
	case "fileChange":
		var it FileChangeThreadItem
		if decode(raw, &it) {
			done(it.Status == "completed", "")
		}
	case "dynamicToolCall", "collabAgentToolCall", "imageGeneration", "imageView":
		var it struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(raw, &it)
		done(it.Status == "" || it.Status == "completed", "")
	}
}

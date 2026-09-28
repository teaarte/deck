package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AskTool asks the owner a blocking question for a provider without a question tool of its own.
const AskTool = "ask"

var askSchema = map[string]any{"type": "object", "required": []string{"question"}, "properties": map[string]any{
	"question": map[string]any{"type": "string"},
	"options": map[string]any{"type": "array", "description": "2 to 4 choices, the recommended one first",
		"items": map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"}}}}}}

func askTool() *mcp.Tool {
	return &mcp.Tool{Name: AskTool, Title: "Ask the owner",
		Description: "Asks the owner a question you cannot go on without and waits for the answer.", InputSchema: askSchema}
}

// ask puts the question to the owner the way the question tool of Claude Code does and gives the answers as text.
func (g *Gateway) ask(ctx context.Context, s *Session, raw json.RawMessage) *mcp.CallToolResult {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return refusal("deck: " + err.Error())
	}
	if q, _ := in["question"].(string); strings.TrimSpace(q) == "" {
		return refusal("deck: ask takes the question in the question field")
	}
	d := g.question(ctx, s, approveInput{ToolName: QuestionTool, Input: map[string]any{"questions": []any{in}}})
	if d.Behavior != "allow" {
		return refusal(d.Message)
	}
	answers, _ := d.UpdatedInput["answers"].(map[string]string)
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("The owner answered.")
	for _, k := range keys {
		fmt.Fprintf(&b, "\n%s: %s", k, answers[k])
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
}

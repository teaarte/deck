package openai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

// questionTool asks the owner a blocking question; the loop hands it to the permission tool of the gateway,
// the way Claude Code hands over its own.
const questionTool = "AskUserQuestion"

var questionSchema = map[string]any{"type": "object", "required": []string{"questions"}, "properties": map[string]any{
	"questions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"question"},
		"properties": map[string]any{
			"question": map[string]any{"type": "string"},
			"options": map[string]any{"type": "array", "description": "2 to 4 choices, the recommended one first",
				"items": map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"}}}}}}}}}

// gatewayTools are the tools of the deck MCP server as function tools; the permission tool is deck's own.
func gatewayTools(ctx context.Context, sess *mcp.ClientSession, permission string) ([]openai.ChatCompletionToolUnionParam, error) {
	var out []openai.ChatCompletionToolUnionParam
	for t, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if t.Name == permission {
			out = append(out, function(questionTool, "Asks the owner a question you cannot go on without, and waits for the answer.", questionSchema))
			continue
		}
		var schema map[string]any
		b, _ := json.Marshal(t.InputSchema)
		_ = json.Unmarshal(b, &schema)
		out = append(out, function(t.Name, t.Description, schema))
	}
	return out, nil
}

func function(name, desc string, schema map[string]any) openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{Name: name, Description: openai.String(desc),
		Parameters: shared.FunctionParameters(schema)})
}

// call makes one tool call of the model through the gateway and gives what the model reads back.
// call makes one tool call and gives its text for the model and whether it succeeded.
func call(ctx context.Context, sess *mcp.ClientSession, permission, name, arguments, id string) (string, bool) {
	args := json.RawMessage(arguments)
	if !json.Valid(args) || len(strings.TrimSpace(arguments)) == 0 {
		args = json.RawMessage(`{}`)
	}
	if name == questionTool {
		return ask(ctx, sess, permission, id, args)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "Error: " + err.Error(), false
	}
	text := contentText(res)
	if res.IsError {
		return "Error: " + text, false
	}
	return text, true
}

func ask(ctx context.Context, sess *mcp.ClientSession, permission, id string, args json.RawMessage) (string, bool) {
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: permission,
		Arguments: map[string]any{"tool_name": questionTool, "input": args, "tool_use_id": id}})
	if err != nil {
		return "Error: " + err.Error(), false
	}
	var d struct {
		Behavior     string `json:"behavior"`
		Message      string `json:"message"`
		UpdatedInput struct {
			Answers map[string]string `json:"answers"`
		} `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(contentText(res)), &d); err != nil || d.Behavior != "allow" {
		if d.Message == "" {
			d.Message = contentText(res)
		}
		return "Error: " + d.Message, false
	}
	b, _ := json.Marshal(d.UpdatedInput.Answers)
	return "The owner answered: " + string(b), true
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			b.WriteString(c.Text)
		case *mcp.ImageContent:
			b.WriteString("[image " + c.MIMEType + " left out]")
		}
	}
	if b.Len() == 0 && res.StructuredContent != nil {
		out, _ := json.Marshal(res.StructuredContent)
		return string(out)
	}
	return b.String()
}

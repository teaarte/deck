package openai

import (
	"context"
	"encoding/json"
	"strings"

	"deck2/internal/provider"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

// askName is the ask tool on the deck server; the model sees it under the name its prompt gives.
var askName = provider.Bare(provider.AskTool)

// gatewayTools are the tools of the deck MCP server as function tools; the permission tool is deck's own.
func gatewayTools(ctx context.Context, sess *mcp.ClientSession, permission string) ([]openai.ChatCompletionToolUnionParam, error) {
	var out []openai.ChatCompletionToolUnionParam
	for t, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if t.Name == permission {
			continue
		}
		var schema map[string]any
		b, _ := json.Marshal(t.InputSchema)
		_ = json.Unmarshal(b, &schema)
		name := t.Name
		if name == askName {
			name = provider.AskTool
		}
		out = append(out, function(name, t.Description, schema))
	}
	return out, nil
}

func function(name, desc string, schema map[string]any) openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{Name: name, Description: openai.String(desc),
		Parameters: shared.FunctionParameters(schema)})
}

// call makes one tool call and gives its text for the model and whether it succeeded.
func call(ctx context.Context, sess *mcp.ClientSession, name, arguments string) (string, bool) {
	args := json.RawMessage(arguments)
	if !json.Valid(args) || len(strings.TrimSpace(arguments)) == 0 {
		args = json.RawMessage(`{}`)
	}
	if name == provider.AskTool {
		name = askName
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

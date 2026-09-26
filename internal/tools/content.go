package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/mcphost"
)

// failed settles the intent of an MCP call that failed: rejected when it never reached the server, done
// when the server answered, unknown when the connection was lost while the call was out.
func failed(ctx context.Context, gw *gateway.Gateway, intent int64, name string, err error) error {
	var ns *mcphost.ErrNotStarted
	switch {
	case errors.As(err, &ns):
		if intent != 0 {
			gw.Finish(ctx, intent, "rejected", "", err.Error())
		}
	case mcphost.Answered(err):
		if intent != 0 {
			gw.Finish(ctx, intent, "done", "", err.Error())
		}
	case intent != 0:
		gw.Lost(ctx, intent)
		return fmt.Errorf("deck: %s: the outcome is unknown and the owner decides whether to repeat it: %v", name, err)
	}
	return fmt.Errorf("deck: %s: %v", name, err)
}

// Content turns a tool result into what the caller gets: its structured content, or its text with binary
// parts saved as files in the results folder of the process; an error result is an error.
func Content(dirs files.Dirs, process int64, tool string, res *mcp.CallToolResult) (any, error) {
	var text []string
	var saved []map[string]any
	keep := func(data []byte, mime string) {
		if process == 0 {
			text = append(text, "(a "+mime+" file was left out: only a process with a results folder takes files)")
			return
		}
		name, n, err := dirs.Save(process, tool, mime, strings.NewReader(string(data)), maxFile)
		if err != nil {
			text = append(text, err.Error())
			return
		}
		saved = append(saved, map[string]any{"path": name, "type": mime, "size": n})
	}
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			text = append(text, v.Text)
		case *mcp.ImageContent:
			keep(v.Data, v.MIMEType)
		case *mcp.AudioContent:
			keep(v.Data, v.MIMEType)
		case *mcp.ResourceLink:
			text = append(text, v.URI)
		case *mcp.EmbeddedResource:
			if v.Resource == nil {
				continue
			}
			if v.Resource.Blob != nil {
				keep(v.Resource.Blob, v.Resource.MIMEType)
			} else {
				text = append(text, v.Resource.Text)
			}
		}
	}
	joined := strings.Join(text, "\n")
	if res.IsError {
		return nil, fmt.Errorf("deck: %s: %s", tool, truncate(joined, 4000))
	}
	if res.StructuredContent != nil && len(saved) == 0 {
		return res.StructuredContent, nil
	}
	out := map[string]any{"text": joined}
	if len(saved) > 0 {
		out["files"] = saved
	}
	return out, nil
}

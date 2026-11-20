package tools

import (
	"errors"
	"fmt"
	"strings"

	"deck2/internal/home"
	"deck2/internal/untrusted"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/files"
)

// failed is the error of a call of tool that failed with msg, which names the tool once.
func failed(tool, msg string) error {
	if strings.HasPrefix(msg, "deck: ") {
		return errors.New(msg)
	}
	return fmt.Errorf("deck: %s: %s", tool, msg)
}

// Content turns a tool result into what the caller gets: its structured content, or its text with binary
// parts saved as files in the results folder of the process; an error result is an error.
func Content(h home.Layout, process int64, tool string, res *mcp.CallToolResult) (any, error) {
	var text []string
	var saved []map[string]any
	keep := func(data []byte, mime string) {
		if process == 0 {
			text = append(text, "(a "+mime+" file was left out: only a process with a results folder takes files)")
			return
		}
		name, n, err := files.Save(h, process, tool, mime, strings.NewReader(string(data)), maxFile)
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
		return nil, fmt.Errorf("deck: %s: %s", tool, untrusted.Cut(joined, 4000))
	}
	if res.StructuredContent != nil {
		if len(saved) == 0 {
			return res.StructuredContent, nil
		}
		if o, ok := res.StructuredContent.(map[string]any); ok && o["files"] == nil {
			o["files"] = saved
			return o, nil
		}
		return map[string]any{"result": res.StructuredContent, "text": joined, "files": saved}, nil
	}
	out := map[string]any{"text": joined}
	if len(saved) > 0 {
		out["files"] = saved
	}
	return out, nil
}

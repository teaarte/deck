package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// browserTools is a fake Playwright MCP server: one tab, pages are only their URLs; a nocurrent file hides
// which tab is current.
func browserTools(dir string) []fakeTool {
	mode := "headed"
	if slices.Contains(os.Args, "--headless") {
		mode = "headless"
	}
	if slices.Contains(os.Args, "--isolated") && !slices.Contains(os.Args, "--user-data-dir") {
		mode += ":isolated"
	}
	note(dir, "start:"+mode)
	for flag, name := range map[string]string{"--proxy-server": "proxy", "--init-script": "init"} {
		if i := slices.Index(os.Args, flag); i >= 0 && i+1 < len(os.Args) {
			note(dir, name+":"+os.Args[i+1])
		}
	}
	page := "about:blank"
	str := map[string]any{"type": "string"}
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	act := func(name string) fakeTool {
		return fakeTool{name: name, desc: "Acts on the page.", input: obj(map[string]any{"target": str, "text": str}),
			run: func(a map[string]any) *mcp.CallToolResult {
				note(dir, strings.TrimPrefix(name, "browser_")+":"+page)
				if next, err := os.ReadFile(filepath.Join(dir, "goto")); err == nil {
					_ = os.Remove(filepath.Join(dir, "goto"))
					if string(next) == "crash" {
						os.Exit(3)
					}
					page = string(next)
				}
				return text("### Page\n- Page URL: " + page)
			}}
	}
	return []fakeTool{
		{name: "browser_navigate", desc: "Navigate to a URL", input: obj(map[string]any{"url": str}), run: func(a map[string]any) *mcp.CallToolResult {
			page = fmt.Sprint(a["url"])
			note(dir, "navigate:"+page)
			return text("### Page\n- Page URL: " + page)
		}},
		{name: "browser_tabs", desc: "List tabs", input: obj(map[string]any{"action": str, "url": str}), run: func(a map[string]any) *mcp.CallToolResult {
			if a["action"] == "new" {
				page = fmt.Sprint(a["url"])
			}
			shown := page
			if next, err := os.ReadFile(filepath.Join(dir, "flip")); err == nil {
				page = strings.TrimSpace(string(next))
				_ = os.Remove(filepath.Join(dir, "flip"))
			}
			mark := "(current) "
			if _, err := os.Stat(filepath.Join(dir, "nocurrent")); err == nil {
				mark = ""
			}
			return text("### Result\n- 0: " + mark + "[Title (1)](" + shown + ")")
		}},
		{name: "browser_snapshot", desc: "Snapshot", run: func(map[string]any) *mcp.CallToolResult {
			if os.Remove(filepath.Join(dir, "hang")) == nil {
				time.Sleep(time.Minute)
			}
			return text("- button \"Send\" [ref=e2] on " + page)
		}},
		{name: "browser_take_screenshot", desc: "Screenshot", run: func(map[string]any) *mcp.CallToolResult {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "screenshot"}, &mcp.ImageContent{MIMEType: "image/png", Data: []byte("PNG")}}}
		}},
		act("browser_click"), act("browser_type"),
		{name: "browser_file_upload", desc: "Upload", input: obj(map[string]any{"paths": map[string]any{"type": "array", "items": str}}),
			run: func(a map[string]any) *mcp.CallToolResult {
				note(dir, fmt.Sprintf("upload:%d", len(a["paths"].([]any))))
				return text("uploaded")
			}},
		{name: "browser_run_code_unsafe", desc: "Runs code", run: func(map[string]any) *mcp.CallToolResult {
			note(dir, "code")
			return text("ran")
		}},
	}
}

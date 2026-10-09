package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain lets the test binary serve as a fake MCP server (DECK_FAKE_MCP picks which), app-server of Codex or ACP agent.
func TestMain(m *testing.M) {
	if dir := os.Getenv("DECK_FAKE_ACP"); dir != "" {
		serveACP(dir)
		return
	}
	if dir := os.Getenv("DECK_FAKE_CODEX"); dir != "" {
		serveCodex(dir)
		return
	}
	switch os.Getenv("DECK_FAKE_MCP") {
	case "project":
		dir, _ := os.Getwd()
		_ = os.WriteFile(filepath.Join(dir, "pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
		serveFake(projectTools(dir))
	case "browser":
		serveFake(browserTools(os.Getenv("DECK_FAKE_DIR")))
	case "approve":
		if len(os.Args) != 4 {
			fakeFailed(fmt.Errorf("approve takes url, tool and input, got %q", os.Args[1:]))
		}
		askApprove(os.Args[1], os.Args[2], os.Args[3])
	default:
		os.Exit(m.Run())
	}
}

// askApprove calls the permission tool at url about one tool call the way Claude Code does.
func askApprove(url, tool, input string) {
	ctx := context.Background()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "fake-cli", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		fakeFailed(err)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		fakeFailed(err)
	}
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "approve",
		Arguments: map[string]any{"tool_name": tool, "input": in, "tool_use_id": "t1"}}); err != nil {
		fakeFailed(err)
	}
	os.Exit(0)
}

// fakeFailed leaves err where the test of the fake CLI that ran this helper finds it, and exits.
func fakeFailed(err error) {
	fmt.Fprintln(os.Stderr, err)
	if f, ferr := os.OpenFile(os.Getenv("DECK_FAKE_FAILED"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); ferr == nil {
		fmt.Fprintln(f, err)
		_ = f.Close()
	}
	os.Exit(1)
}

type fakeTool struct {
	name, desc string
	input      map[string]any
	run        func(args map[string]any) *mcp.CallToolResult
}

func serveFake(tools []fakeTool) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
	hidden, _ := os.ReadFile("hide")
	for _, t := range tools {
		if slices.Contains(strings.Fields(string(hidden)), t.name) {
			continue
		}
		in := t.input
		if in == nil {
			in = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		srv.AddTool(&mcp.Tool{Name: t.name, Description: t.desc, InputSchema: in},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any
				_ = json.Unmarshal(req.Params.Arguments, &args)
				return t.run(args), nil
			})
	}
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

var fakeMu sync.Mutex

// note appends a line to the log of the fake server in dir, so a test sees what the server really did.
func note(dir, line string) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintln(f, line)
		_ = f.Close()
	}
}

// fakeLog is what the fake server in dir did, one line per call.
func fakeLog(dir string) []string {
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	return strings.Fields(string(b))
}

func projectTools(dir string) []fakeTool {
	desc, _ := os.ReadFile(filepath.Join(dir, "send-description"))
	if len(desc) == 0 {
		desc = []byte("Sends a message.")
	}
	str := map[string]any{"type": "string"}
	return []fakeTool{
		{name: "reach", desc: "Reaches an address through the proxy it is given.", input: map[string]any{"type": "object", "properties": map[string]any{"url": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				tr := &http.Transport{}
				if p := os.Getenv("HTTP_PROXY"); p != "" {
					if !strings.Contains(p, "://") {
						p = "http://" + p
					}
					u, _ := url.Parse(p)
					tr.Proxy = http.ProxyURL(u)
				}
				r, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(fmt.Sprint(a["url"]))
				if err != nil {
					return text("unreached: " + err.Error())
				}
				r.Body.Close()
				return text(fmt.Sprint("status ", r.StatusCode))
			}},
		{name: "lookup", desc: "Looks a word up.", input: map[string]any{"type": "object", "properties": map[string]any{"word": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				note(dir, "lookup:"+fmt.Sprint(a["word"]))
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "found"}},
					StructuredContent: map[string]any{"word": a["word"], "found": true}}
			}},
		{name: "send", desc: string(desc), input: map[string]any{"type": "object", "properties": map[string]any{"to": str},
			"required": []string{"to"}},
			run: func(a map[string]any) *mcp.CallToolResult {
				note(dir, "send:"+fmt.Sprint(a["to"]))
				return text("sent to " + fmt.Sprint(a["to"]))
			}},
		{name: "peek", desc: "Reads a file.", input: map[string]any{"type": "object", "properties": map[string]any{"path": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				_, err := os.ReadFile(fmt.Sprint(a["path"]))
				return text(fmt.Sprintf("read=%v", err == nil))
			}},
		{name: "dial", desc: "Connects to a local socket.", input: map[string]any{"type": "object", "properties": map[string]any{"path": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				c, err := net.Dial("unix", fmt.Sprint(a["path"]))
				if err == nil {
					_ = c.Close()
				}
				return text(fmt.Sprintf("connected=%v", err == nil))
			}},
		{name: "env", desc: "Tells its environment.", run: func(map[string]any) *mcp.CallToolResult {
			return text(fmt.Sprintf("token=%s len=%d leak=%s", os.Getenv("FAKE_TOKEN"), len(os.Getenv("FAKE_TOKEN")), os.Getenv("DECK_LEAK")))
		}},
		{name: "hidden", desc: "Not in the tool filter.", run: func(map[string]any) *mcp.CallToolResult {
			note(dir, "hidden")
			return text("hidden")
		}},
		{name: "picture", desc: "Draws a picture.", run: func(map[string]any) *mcp.CallToolResult {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "a dot"},
				&mcp.ImageContent{MIMEType: "image/png", Data: []byte("\x89PNG fake")}}}
		}},
		{name: "vanish", desc: "Exits while making the call.", input: map[string]any{"type": "object", "properties": map[string]any{"to": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				note(dir, "vanish:"+fmt.Sprint(a["to"]))
				os.Exit(3)
				return nil
			}},
	}
}

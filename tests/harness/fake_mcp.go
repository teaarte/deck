package harness

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// alive is the file a fake server of a project holds locked while it runs; a pid would not do, since a sandbox with
// a pid namespace of its own gives the server a pid of that namespace.
var alive *os.File

// Stopped reports whether no fake server holds the lock of the file f, opened before the server could go.
func Stopped(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// Serve serves the fake the environment names, an MCP server (DECK_FAKE_MCP picks which), the app-server of Codex,
// an ACP agent or a model, and reports whether it named one.
func Serve() bool {
	if dir := os.Getenv("DECK_FAKE_ACP"); dir != "" {
		serveACP(dir)
		return true
	}
	if addr := os.Getenv("DECK_FAKE_RESPONSES"); addr != "" {
		serveResponses(addr)
	}
	if dir := os.Getenv("DECK_FAKE_CODEX"); dir != "" {
		serveCodex(dir)
		return true
	}
	var err error
	switch os.Getenv("DECK_FAKE_MCP") {
	case "project":
		dir, _ := os.Getwd()
		_ = os.WriteFile(filepath.Join(dir, "pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
		if alive, err = os.OpenFile(filepath.Join(dir, "alive"), os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			_ = syscall.Flock(int(alive.Fd()), syscall.LOCK_EX)
		}
		serveFake(projectTools(dir))
	case "browser":
		serveFake(browserTools(os.Getenv("DECK_FAKE_DIR")))
	case "approve":
		if len(os.Args) != 4 {
			fakeFailed(fmt.Errorf("approve takes url, tool and input, got %q", os.Args[1:]))
		}
		askApprove(os.Args[1], os.Args[2], os.Args[3])
	default:
		return false
	}
	return true
}

// Main runs the tests of a package of scenarios; started as a fake, the test binary serves it instead.
func Main(m *testing.M) {
	if Serve() {
		return
	}
	TrustTestTLS()
	os.Exit(runWithFakes(m))
}

// Fakes is the program that serves the fakes: this test binary, or the command fakes built without the race
// detector, whose every start would cost a second.
var Fakes string

func runWithFakes(m *testing.M) int {
	Fakes, _ = os.Executable()
	if built := os.Getenv("DECK_FAKES"); built != "" {
		Fakes = built
	} else if raceOn {
		dir, err := os.MkdirTemp("", "deck-fakes-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		build := exec.Command("go", "build", "-o", filepath.Join(dir, "fakes"), "deck2/tests/harness/fakes")
		build.Env = append(os.Environ(), "GOFLAGS=")
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build the fakes: %v\n%s", err, out)
			return 1
		}
		Fakes = filepath.Join(dir, "fakes")
	}
	return m.Run()
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

func Text(s string) *mcp.CallToolResult {
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

// FakeLog is what the fake server in dir did, one line per call.
func FakeLog(dir string) []string {
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	return strings.Fields(string(b))
}

// tunnels are the tunnels the hold tool keeps open.
var tunnels []net.Conn

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
					return Text("unreached: " + err.Error())
				}
				r.Body.Close()
				return Text(fmt.Sprint("status ", r.StatusCode))
			}},
		{name: "hold", desc: "Opens a tunnel through the proxy it is given and keeps it.", input: map[string]any{"type": "object", "properties": map[string]any{"addr": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				u, err := url.Parse(os.Getenv("HTTP_PROXY"))
				if err != nil {
					return Text("unreached: " + err.Error())
				}
				c, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
				if err != nil {
					return Text("unreached: " + err.Error())
				}
				pass, _ := u.User.Password()
				auth := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pass))
				addr := fmt.Sprint(a["addr"])
				fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", addr, addr, auth)
				r, err := http.ReadResponse(bufio.NewReader(c), nil)
				if err != nil || r.StatusCode != http.StatusOK {
					return Text(fmt.Sprint("unreached: ", err, r))
				}
				tunnels = append(tunnels, c)
				return Text("held")
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
				return Text("sent to " + fmt.Sprint(a["to"]))
			}},
		{name: "peek", desc: "Reads a file.", input: map[string]any{"type": "object", "properties": map[string]any{"path": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				_, err := os.ReadFile(fmt.Sprint(a["path"]))
				return Text(fmt.Sprintf("read=%v", err == nil))
			}},
		{name: "dial", desc: "Connects to a local socket.", input: map[string]any{"type": "object", "properties": map[string]any{"path": str}},
			run: func(a map[string]any) *mcp.CallToolResult {
				c, err := net.Dial("unix", fmt.Sprint(a["path"]))
				if err == nil {
					_ = c.Close()
				}
				return Text(fmt.Sprintf("connected=%v", err == nil))
			}},
		{name: "env", desc: "Tells its environment.", run: func(map[string]any) *mcp.CallToolResult {
			return Text(fmt.Sprintf("token=%s len=%d leak=%s", os.Getenv("FAKE_TOKEN"), len(os.Getenv("FAKE_TOKEN")), os.Getenv("DECK_LEAK")))
		}},
		{name: "hidden", desc: "Not in the tool filter.", run: func(map[string]any) *mcp.CallToolResult {
			note(dir, "hidden")
			return Text("hidden")
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

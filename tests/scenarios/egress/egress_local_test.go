package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAnMCPServerByURLIsNotRedirectedToTheLocalNetwork: a server by URL that redirects to deck's machine or to the
// metadata of a cloud is refused there, and the place it redirects to gets nothing.
func TestAnMCPServerByURLIsNotRedirectedToTheLocalNetwork(t *testing.T) {
	t.Parallel()
	inner := mcp.NewServer(&mcp.Implementation{Name: "inner", Version: "0"}, nil)
	inner.AddTool(&mcp.Tool{Name: "ping", Description: "Answers pong.", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return harness.Text("pong"), nil
		})
	var reached atomic.Int64
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return inner }, nil).ServeHTTP(w, r)
	}))
	t.Cleanup(local.Close)
	to := func(target string) *harness.Service {
		return harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
			http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
		})
	}
	loopback, metadata := to(local.URL), to("http://169.254.169.254")
	got := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		for _, tool := range []string{"srv__loopback__ping", "srv__metadata__ping"} {
			out, isErr := c.Tool(ctx, tool, nil)
			got[tool] = fmt.Sprint(isErr, " ", out)
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))
	e.Project("srv", harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "work", "title": "Work", "description": "Works.",
    "input": {"type": "object", "properties": {}}, "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}}],
  "tools": {"mcp": {"servers": {"loopback": {"url": %q, "right": {"ping": "network"}}, "metadata": {"url": %q, "right": {"ping": "network"}}}}}}`,
		loopback.URL+"/mcp", metadata.URL+"/mcp")))
	e.WaitState(e.Start("srv", "work", nil), "done")
	if len(loopback.Requests()) == 0 || len(metadata.Requests()) == 0 {
		t.Fatalf("deck tries both servers: %d %d", len(loopback.Requests()), len(metadata.Requests()))
	}
	for tool, out := range got {
		if !strings.HasPrefix(out, "true") || strings.Contains(out, "pong") {
			t.Errorf("%s is not reached through a redirect: %s", tool, out)
		}
	}
	if n := reached.Load(); n != 0 {
		t.Fatalf("the service on deck's machine got %d requests through the redirect", n)
	}
}

// TestAToolOnALocalAddressNeedsItsModuleToHoldIt: a private address of an HTTP tool is reached only when its module
// holds that address; "*" holds none, nor one that carries a private IPv4 in NAT64 or 6to4.
func TestAToolOnALocalAddressNeedsItsModuleToHoldIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	tool := func(url, rights string) string {
		return harness.Manifest(fmt.Sprintf(`{%s"tools": {"http": [{"name": "get", "description": "Gets.", "method": "GET",
  "url": %q, "input": {"type": "object", "properties": {}}}]}}`, rights, url))
	}
	e.Project("lan", harness.Declared(tool("http://10.0.0.1/x", "")))
	e.Must("deck.right_set", map[string]any{"project": "lan", "right": "network", "subject": "10.0.0.1", "answer": "revoke"}, nil)
	if code, out := e.Call("lan.get", map[string]any{}); code != 403 || !strings.Contains(string(out), "not a host this module holds the network of") {
		t.Fatalf("a tool on 10.0.0.1 its module does not hold is refused: %d %s", code, out)
	}
	for project, url := range map[string]string{"nat": "http://[64:ff9b::a9fe:a9fe]/latest", "six": "http://[2002:a9fe:a9fe::1]/latest",
		"wide": "http://10.0.0.1/x"} {
		code, out := e.Call("deck.project_create", map[string]any{"name": project, "manifest": tool(url, `"rights": [{"right": "network", "what": ["*"], "why": "reads it"}], `)})
		if code != 422 || !strings.Contains(string(out), "it needs the right network on") {
			t.Errorf("%s is no public address, whatever \"*\" lets in: %d %s", url, code, out)
		}
	}
	e.Project("box", harness.ScriptManifest("collect", "", `, "rights": [{"right": "network", "what": ["*"], "why": "reads it"}]`))
	e.SaveScript("box", "scripts/main.py", harness.PythonLib+`
print(json.dumps({"nat": fetch("http://[64:ff9b::a9fe:a9fe]/latest"), "six": fetch("http://[2002:a9fe:a9fe::1]/latest")}))
`)
	p := e.WaitState(e.Start("box", "collect", nil), "done", "error")
	var got struct{ Nat, Six int }
	if err := json.Unmarshal(p.Result, &got); err != nil || got.Nat != 403 || got.Six != 403 {
		t.Fatalf("a script reaches no IPv4 of the local network wrapped in IPv6: %+v %s", p, p.Result)
	}
}

// TestAGrantOfEveryAddressSaysItReachesTheLocalNetwork: a host that holds addresses beyond the internet, such as
// 0.0.0.0/0, is the local network in what deck tells the owner.
func TestAGrantOfEveryAddressSaysItReachesTheLocalNetwork(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "all", "manifest": harness.ScriptManifest("collect", "",
		`, "rights": [{"right": "network", "what": ["0.0.0.0/0", "example.com", "192.168.1.7"], "why": "reads it"}]`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	r := e.ProjectRow("all").Rights
	for _, want := range []string{"its programs reach 0.0.0.0/0 on your local network and may send its data there",
		"its programs reach example.com and may send its data there", "its programs reach 192.168.1.7 on your local network and may send its data there"} {
		if !slices.Contains(r, want) {
			t.Errorf("the rights show %q: %v", want, r)
		}
	}
}

// TestABoxWithEveryHostReachesNoGatewayOfItsHost: "*" is the internet, not the machine deck runs on: the name
// host.docker.internal, its address in OrbStack, OrbStack's machines and the same in IPv6 forms are refused at the door.
func TestABoxWithEveryHostReachesNoGatewayOfItsHost(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("box", harness.ScriptManifest("collect", "", `, "rights": [{"right": "network", "what": ["*"], "why": "reads it"}]`))
	e.SaveScript("box", "scripts/main.py", harness.PythonLib+`
hosts = ["host.docker.internal:7794", "0.250.250.254:7700", "198.19.249.3", "[::ffff:0.250.250.254]:7700", "[64:ff9b::fa:fafe]:7700",
    "[2002:fa:fafe::1]:7700"]
print(json.dumps({h: fetch("http://" + h + "/") for h in hosts}))
`)
	p := e.WaitState(e.Start("box", "collect", nil), "done", "error")
	var got map[string]any
	if err := json.Unmarshal(p.Result, &got); err != nil || len(got) != 6 {
		t.Fatalf("the script answers each host: %+v %s", p, p.Result)
	}
	for host, code := range got {
		// A name that does not resolve, as host.docker.internal off Docker, is a 502 of the door: it is not reached either.
		if code != float64(403) && !(strings.HasPrefix(host, "host.") && code == float64(502)) {
			t.Errorf("the door refuses %s to a box with \"*\": %v", host, code)
		}
	}
}

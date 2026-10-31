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
	e.Project("srv", harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "work", "title": "Work", "description": "Works.", "class": "internal",
    "input": {"type": "object", "properties": {}}, "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}}],
  "tools": {"mcp": {"servers": {"loopback": {"url": %q, "classes": {"ping": "internal"}}, "metadata": {"url": %q, "classes": {"ping": "internal"}}}}}}`,
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

// TestAToolOnALocalAddressNeedsTheDomainsToNameIt: the host of an HTTP tool's URL is reached by itself only when
// public; a private address, or one that carries a private IPv4 in NAT64 or 6to4, is refused before any dial.
func TestAToolOnALocalAddressNeedsTheDomainsToNameIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	tool := func(project, url, domains string) {
		e.Project(project, harness.Manifest(fmt.Sprintf(`{%s"tools": {"http": [{"name": "get", "description": "Gets.", "method": "GET",
  "url": %q, "class": "internal", "input": {"type": "object", "properties": {}}}]}}`, domains, url)))
	}
	tool("lan", "http://10.0.0.1/x", "")
	if code, out := e.Call("lan.get", map[string]any{}); code != 403 || !strings.Contains(string(out), "not in the domains") {
		t.Fatalf("a tool on 10.0.0.1 without domains is refused: %d %s", code, out)
	}
	for project, url := range map[string]string{"nat": "http://[64:ff9b::a9fe:a9fe]/latest", "six": "http://[2002:a9fe:a9fe::1]/latest",
		"wide": "http://10.0.0.1/x"} {
		tool(project, url, `"domains": {"read": ["*"]}, `)
		if code, out := e.Call(project+".get", map[string]any{}); code != 403 || !strings.Contains(string(out), "not a public address") {
			t.Errorf("%s is no public address, whatever \"*\" lets in: %d %s", url, code, out)
		}
	}
	e.Project("box", harness.ScriptManifest("collect", "", `, "domains": {"read": ["*"]}`))
	e.SaveScript("box", "scripts/main.py", harness.PythonLib+`
print(json.dumps({"nat": fetch("http://[64:ff9b::a9fe:a9fe]/latest"), "six": fetch("http://[2002:a9fe:a9fe::1]/latest")}))
`)
	p := e.WaitState(e.Start("box", "collect", nil), "done", "error")
	var got struct{ Nat, Six int }
	if err := json.Unmarshal(p.Result, &got); err != nil || got.Nat != 403 || got.Six != 403 {
		t.Fatalf("a script reaches no IPv4 of the local network wrapped in IPv6: %+v %s", p, p.Result)
	}
}

// TestAGrantOfEveryAddressSaysItReachesTheLocalNetwork: a domain that holds addresses beyond the internet, such as
// 0.0.0.0/0, is a right of its own the owner sees: the local network.
func TestAGrantOfEveryAddressSaysItReachesTheLocalNetwork(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*")
	e.As(tok).Call("deck.project_create", map[string]any{"name": "all", "manifest": harness.ScriptManifest("collect", "",
		`, "domains": {"read": ["0.0.0.0/0", "example.com"], "act": ["192.168.1.7"]}`)})
	r := e.ProjectRow("all").Rights
	for _, want := range []string{"read domain: 0.0.0.0/0", "local network: 0.0.0.0/0", "read domain: example.com", "local network: 192.168.1.7"} {
		if !slices.Contains(r, want) {
			t.Errorf("the rights show %q: %v", want, r)
		}
	}
	if slices.Contains(r, "local network: example.com") {
		t.Fatalf("a name is no local network: %v", r)
	}
}

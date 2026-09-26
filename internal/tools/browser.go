package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/registry"
)

// BrowserKey names the browser server in the host.
const BrowserKey = "browser"

// Tools of the browser server that only read; the ones in browserActs click, type or submit, and the rest
// (running code in the page or the server, request details with cookies) are never offered.
var (
	browserReads = []string{"browser_navigate", "browser_navigate_back", "browser_snapshot", "browser_find", "browser_take_screenshot",
		"browser_wait_for", "browser_tabs", "browser_hover", "browser_close", "browser_console_messages", "browser_resize"}
	browserActs = []string{"browser_click", "browser_type", "browser_fill_form", "browser_select_option", "browser_press_key",
		"browser_drag", "browser_drop", "browser_file_upload", "browser_handle_dialog"}
)

// Browser is the browser MCP server with deck's own profile: reading pages of the read domains, acting
// only on act domains unless the owner allows it. One call runs at a time, together with its domain check.
type Browser struct {
	GW   *gateway.Gateway
	Host *mcphost.Host
	Dirs files.Dirs
	Home string
	// Argv starts the server; deck adds the profile, the output folder and headless mode.
	Argv      []string
	Manifests func(ctx context.Context, project int64) (*manifest.Manifest, error)

	mu     sync.Mutex
	headed bool
	listMu sync.Mutex
	cmds   []*registry.Command
}

func (b *Browser) dir(name string) string { return filepath.Join(b.Home, "browser", name) }

func (b *Browser) spec() mcphost.Spec {
	args := append(slices.Clone(b.Argv[1:]), "--user-data-dir", b.dir("profile"), "--output-dir", b.dir("out"))
	if !b.headed {
		args = append(args, "--headless")
	}
	var env []string
	for _, kv := range os.Environ() {
		if baseEnv.MatchString(kv) {
			env = append(env, kv)
		}
	}
	return mcphost.Spec{Command: b.Argv[0], Args: args, Env: env, Dir: b.dir("work")}
}

// Load reads the tools the server listed last time, so agents see them before the browser starts.
func (b *Browser) Load() {
	raw, err := os.ReadFile(b.dir("tools.json"))
	if err != nil {
		return
	}
	var list []*mcp.Tool
	if json.Unmarshal(raw, &list) == nil {
		b.build(list)
	}
}

// Keep stores the tools the server listed after it started.
func (b *Browser) Keep(list []*mcp.Tool) {
	if raw, err := json.Marshal(list); err == nil && os.MkdirAll(b.dir(""), 0o700) == nil {
		_ = os.WriteFile(b.dir("tools.json"), raw, 0o600)
	}
	b.build(list)
}

// Ensure starts the browser once when deck does not know its tools yet.
func (b *Browser) Ensure(ctx context.Context) {
	b.listMu.Lock()
	known := len(b.cmds) > 0
	b.listMu.Unlock()
	if known || len(b.Argv) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.Host.Tools(ctx, BrowserKey, b.spec())
}

// Commands are the browser tools as builtin registry commands.
func (b *Browser) Commands(context.Context) []*registry.Command {
	b.listMu.Lock()
	defer b.listMu.Unlock()
	return b.cmds
}

func (b *Browser) build(list []*mcp.Tool) {
	var out []*registry.Command
	for _, t := range list {
		act := slices.Contains(browserActs, t.Name)
		if !act && !slices.Contains(browserReads, t.Name) {
			continue
		}
		input, _ := json.Marshal(t.InputSchema)
		c := &registry.Command{Name: registry.Builtin + "." + t.Name, Title: t.Name, Description: t.Description, Class: registry.Internal,
			Input: input, Callers: []string{registry.Agent}, Loose: true, Handler: b.reader(t.Name)}
		if act {
			c.Class, c.Journals, c.Handler = registry.External, true, b.actor(t.Name)
			c.Allowed = b.allowed
			c.Fingerprint = func(ctx context.Context) string {
				b.mu.Lock()
				defer b.mu.Unlock()
				page, _ := b.current(ctx)
				return page
			}
		}
		if registry.Prepare(c) == nil {
			out = append(out, c)
		}
	}
	b.listMu.Lock()
	b.cmds = out
	b.listMu.Unlock()
}

func (b *Browser) call(ctx context.Context, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	return b.Host.Call(ctx, BrowserKey, b.spec(), tool, args, nil)
}

// current is the URL of the page the browser shows; b.mu is held.
func (b *Browser) current(ctx context.Context) (string, error) {
	res, err := b.call(ctx, "browser_tabs", json.RawMessage(`{"action":"list"}`))
	if err != nil {
		return "", err
	}
	for _, c := range res.Content {
		t, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		for _, line := range strings.Split(t.Text, "\n") {
			if !strings.Contains(line, "(current)") {
				continue
			}
			line = strings.TrimSpace(line)
			if i := strings.LastIndex(line, "]("); i >= 0 && strings.HasSuffix(line, ")") {
				return line[i+2 : len(line)-1], nil
			}
		}
	}
	return "", nil
}

// on reports whether a page URL is on one of the domains; only http and https pages are.
func on(domains []string, page string) bool {
	u, err := url.Parse(page)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return gateway.Reachable(domains, net.JoinHostPort(u.Hostname(), port))
}

// allowed lets an action run without the owner when the project lists the tool or the page is on an act domain.
func (b *Browser) allowed(ctx context.Context, act registry.Actor, listed bool) (bool, error) {
	if listed {
		return true, nil
	}
	m, err := b.Manifests(ctx, act.ProjectID)
	if err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	page, err := b.current(ctx)
	return err == nil && on(m.Domains.Act, page), nil
}

func (b *Browser) reader(tool string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		var args struct{ URL, Action string }
		_ = json.Unmarshal(raw, &args)
		if tool == "browser_navigate" || tool == "browser_tabs" && args.Action == "new" && args.URL != "" {
			m, err := b.Manifests(ctx, act.ProjectID)
			if err != nil {
				return nil, err
			}
			if !on(append(slices.Clone(m.Domains.Read), m.Domains.Act...), args.URL) {
				return nil, registry.Refuse("deck: %s is not on a read or act domain of this project; the owner adds domains to its manifest (\"*\" in read reads any site)", args.URL)
			}
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		res, err := b.call(ctx, tool, raw)
		if err != nil {
			return nil, fmt.Errorf("deck: %s: %v", tool, err)
		}
		if tool != "browser_close" && tool != "browser_resize" && !(tool == "browser_tabs" && args.Action == "list") {
			if page, off := b.offDomains(ctx, act.ProjectID); off {
				return nil, registry.Refuse("deck: the browser shows %s, which is not on a read or act domain of this project; what it shows is withheld", page)
			}
		}
		return Content(b.Dirs, act.ProcessID, tool, res)
	}
}

// offDomains reports the page the browser shows when it is off the project's read and act domains; b.mu is held.
func (b *Browser) offDomains(ctx context.Context, project int64) (string, bool) {
	m, err := b.Manifests(ctx, project)
	page, perr := b.current(ctx)
	if err != nil || perr != nil {
		return page, true
	}
	return page, !on(append(slices.Clone(m.Domains.Read), m.Domains.Act...), page)
}

// Login opens a window of deck's browser at the page, for the owner to sign in with deck's profile.
func (b *Browser) Login(ctx context.Context, page string) error {
	if len(b.Argv) == 0 {
		return registry.Invalid("deck: no browser is set up")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Host.Stop(BrowserKey)
	b.headed = true
	args, _ := json.Marshal(map[string]string{"url": page})
	_, err := b.call(ctx, "browser_navigate", args)
	return err
}

// LoginDone closes the window; the next call starts the browser headless with the same profile.
func (b *Browser) LoginDone() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Host.Stop(BrowserKey)
	b.headed = false
}

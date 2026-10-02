package tools

import (
	"cmp"
	"context"
	"deck2/internal/home"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/docs"
	"deck2/internal/store"
	"deck2/internal/untrusted"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/registry"
)

// BrowserKey names the browser server with deck's profile; OpenBrowserKey the one without sign-ins, which
// projects that read any site get.
const (
	BrowserKey     = "browser"
	OpenBrowserKey = "browser-open"
)

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
	GW    *gateway.Gateway
	Host  *mcphost.Host
	Store *store.Store
	Dirs  files.Dirs
	Home  string
	// Argv starts the server; deck adds the profile, the output folder and headless mode.
	Argv []string
	// Ctx ends the proxy the browser without sign-ins goes through.
	Ctx context.Context

	proxyOnce sync.Once
	proxyAt   string
	// allow are the domains of the project whose call the browser without sign-ins runs now.
	allow atomic.Pointer[[]string]

	mu     sync.Mutex
	headed bool
	listMu sync.Mutex
	cmds   []*registry.Command
}

func (b *Browser) dir(name string) string { return filepath.Join(home.Layout(b.Home).Browser(), name) }

func (b *Browser) spec(open bool) mcphost.Spec {
	args := append(slices.Clone(b.Argv[1:]), "--user-data-dir", b.dir("profile"), "--output-dir", b.dir("out"))
	if open {
		args = append(slices.Clone(b.Argv[1:]), "--isolated", "--output-dir", b.dir("open-out"),
			"--proxy-server", "http://"+cmp.Or(b.proxy(), "127.0.0.1:1"), "--proxy-bypass", "<-loopback>")
	}
	if !b.headed || open {
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

// proxy is where the browser without sign-ins reaches the web: a host reached by "*" is dialled at the public
// address deck resolved, so a name cannot turn into a local address after the check.
func (b *Browser) proxy() string {
	b.proxyOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			slog.Error("browser proxy", "err", err)
			return
		}
		b.proxyAt = ln.Addr().String()
		srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := b.allow.Load()
			if d == nil {
				http.Error(w, "deck: no project runs the browser", http.StatusForbidden)
				return
			}
			gateway.Egress(b.Ctx, w, r, *d)
		})}
		go func() { <-b.Ctx.Done(); _ = srv.Close() }()
		go func() { _ = srv.Serve(ln) }()
	})
	return b.proxyAt
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
	_, _ = b.Host.Tools(ctx, BrowserKey, b.spec(false))
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
			c.Fingerprint = func(ctx context.Context, project int64) string {
				m, err := docs.ManifestAt(ctx, b.Store.Q, project, 0)
				if err != nil {
					return ""
				}
				b.mu.Lock()
				defer b.mu.Unlock()
				page, _ := b.current(ctx, readsAnySite(m))
				return page
			}
			c.Target = func(page string) string {
				if page == "" {
					return "on a page deck could not tell"
				}
				return "on " + untrusted.Cut(page, 300)
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

// readsAnySite says a project has "*" in read: it gets the browser without the owner's sign-ins.
func readsAnySite(m *manifest.Manifest) bool { return slices.Contains(m.Domains.Read, "*") }

func (b *Browser) call(ctx context.Context, open bool, tool string, args json.RawMessage) (*mcp.CallToolResult, error) {
	if open {
		return b.Host.Call(ctx, OpenBrowserKey, b.spec(true), tool, args, nil)
	}
	return b.Host.Call(ctx, BrowserKey, b.spec(false), tool, args, nil)
}

// current is the URL of the page the browser shows; b.mu is held.
func (b *Browser) current(ctx context.Context, open bool) (string, error) {
	res, err := b.call(ctx, open, "browser_tabs", json.RawMessage(`{"action":"list"}`))
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

// allowed lets an action run without the owner when the page is on an act domain.
func (b *Browser) allowed(ctx context.Context, act registry.Actor, _ bool) (bool, error) {
	m, err := docs.ManifestAt(ctx, b.Store.Q, act.ProjectID, 0)
	if err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.serve(m)
	page, err := b.current(ctx, readsAnySite(m))
	return err == nil && on(m.Domains.Act, page), nil
}

func (b *Browser) reader(tool string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		var args struct{ URL, Action string }
		_ = json.Unmarshal(raw, &args)
		m, err := docs.ManifestAt(ctx, b.Store.Q, act.ProjectID, 0)
		if err != nil {
			return nil, err
		}
		if tool == "browser_navigate" || tool == "browser_tabs" && args.Action == "new" && args.URL != "" {
			if !on(append(slices.Clone(m.Domains.Read), m.Domains.Act...), args.URL) {
				return nil, registry.Refuse("deck: %s is not on a read or act domain of this project; the owner adds domains to its manifest (\"*\" in read reads any site)", args.URL)
			}
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.serve(m)
		res, err := b.call(ctx, readsAnySite(m), tool, raw)
		if err != nil {
			return nil, fmt.Errorf("deck: %s: %v", tool, err)
		}
		if tool == "browser_tabs" && args.Action == "list" {
			ownTabs(res, append(slices.Clone(m.Domains.Read), m.Domains.Act...))
		} else if tool != "browser_close" && tool != "browser_resize" {
			if page, off := b.offDomains(ctx, act.ProjectID); off {
				return nil, registry.Refuse("deck: the browser shows %s, which is not on a read or act domain of this project; what it shows is withheld", untrusted.Line(page, 300))
			}
		}
		return Content(b.Dirs, act.ProcessID, tool, res)
	}
}

// serve lets the proxy carry what the project's domains reach; b.mu is held.
func (b *Browser) serve(m *manifest.Manifest) {
	d := append(slices.Clone(m.Domains.Read), m.Domains.Act...)
	b.allow.Store(&d)
}

// ownTabs leaves out of a tab list the pages off the domains, which may be other projects' or the owner's.
func ownTabs(res *mcp.CallToolResult, domains []string) {
	for _, c := range res.Content {
		t, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		lines := strings.Split(t.Text, "\n")
		for i, line := range lines {
			trim := strings.TrimSpace(line)
			if j := strings.LastIndex(trim, "]("); j >= 0 && strings.HasSuffix(trim, ")") && !on(domains, trim[j+2:len(trim)-1]) {
				lines[i] = "- (a tab off the domains of this project)"
			}
		}
		t.Text = strings.Join(lines, "\n")
	}
}

// offDomains reports the page the browser shows when it is off the project's read and act domains; b.mu is held.
func (b *Browser) offDomains(ctx context.Context, project int64) (string, bool) {
	m, err := docs.ManifestAt(ctx, b.Store.Q, project, 0)
	if err != nil {
		return "", true
	}
	page, perr := b.current(ctx, readsAnySite(m))
	if perr != nil {
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
	_, err := b.call(ctx, false, "browser_navigate", args)
	return err
}

// LoginDone closes the window; the next call starts the browser headless with the same profile.
func (b *Browser) LoginDone() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Host.Stop(BrowserKey)
	b.headed = false
}

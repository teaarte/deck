package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/boxes"
	"deck2/internal/docs"
	"deck2/internal/egress"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/untrusted"
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
	Home  home.Layout
	// Argv starts the server; deck adds the profile, the output folder and headless mode.
	Argv []string

	mu     sync.Mutex
	headed bool
	listMu sync.Mutex
	cmds   []*registry.Command
}

func (b *Browser) dir(name string) string { return filepath.Join(b.Home.Browser(), name) }

func (b *Browser) spec(open bool) mcphost.Spec {
	box, key := boxes.For(sandbox.KindBrowser, nil, nil, false, nil), BrowserKey
	box.Home = b.dir("home")
	args := append(slices.Clone(b.Argv[1:]), "--user-data-dir", b.dir("profile"), "--output-dir", b.dir("out"))
	box.Write = []string{b.dir("profile"), b.dir("out")}
	if open {
		key, box.Home, box.Read = OpenBrowserKey, b.dir("open-home"), []string{b.noWebRTC()}
		args = append(slices.Clone(b.Argv[1:]), "--isolated", "--output-dir", b.dir("open-out"), "--init-script", box.Read[0])
		box.Write = []string{b.dir("open-out")}
	}
	args = append(args, "--config", filepath.Join(box.Home, "config.json"))
	if !b.headed || open {
		args = append(args, "--headless")
	} else {
		env, read := sandbox.Display()
		box.Env, box.Read = append(box.Env, env...), append(box.Read, read...)
	}
	box.Argv, box.Dir, box.Files = append([]string{b.Argv[0]}, args...), b.dir("work"), map[string]string{"config.json": browserConfig()}
	box.Mark = b.Host.Sandbox.Mark(sandbox.KindBrowser, key)
	return mcphost.Spec{Box: box}
}

// proxyConfig is the config of Playwright MCP that sends the browser through the door of its box with its key; the
// door reaches the public internet only, so a name cannot turn into a local address after the check.
const proxyConfig = `{"browser": {"launchOptions": {%s"proxy": {"server": "http://{door.addr}", "bypass": "<-loopback>", "username": "deck", "password": "{door.key}"}}}}`

// browserConfig is proxyConfig; on Linux Chrome runs without its own sandbox, which needs a user namespace the box
// does not give: the box is the sandbox there.
func browserConfig() string {
	if runtime.GOOS == "linux" {
		return fmt.Sprintf(proxyConfig, `"chromiumSandbox": false, `)
	}
	return fmt.Sprintf(proxyConfig, "")
}

// noWebRTC is a script run in every page of the browser without sign-ins before the page's own: WebRTC sends
// UDP past the proxy, and Chrome ignores the switch that would stop it.
func (b *Browser) noWebRTC() string {
	path := b.dir("open-no-webrtc.js")
	if os.MkdirAll(b.dir(""), 0o700) != nil || home.WriteFile(path, []byte(noWebRTC)) != nil {
		slog.Error("browser init script", "path", path)
	}
	return path
}

const noWebRTC = `for (const k of ['RTCPeerConnection', 'webkitRTCPeerConnection', 'RTCDataChannel', 'RTCSessionDescription', 'RTCIceCandidate']) {
  try { delete window[k]; } catch {}
  try { Object.defineProperty(window, k, { value: undefined, configurable: false, writable: false }); } catch {}
}`

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
		_ = home.WriteFile(b.dir("tools.json"), raw)
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
		c := registry.Loose(registry.Deck(t.Name), t.Name, t.Description, []string{registry.Agent}, input,
			b.reader(t.Name)).Foreign().Brings(registry.FromOutside)
		if !act {
			c.Stays()
		} else {
			c.Handler = b.actor(t.Name)
			// The page an action acts on is the browser's, which deck looks at only when it may reach out.
			c.Gate.Subject = func(ctx context.Context, at registry.At, _ json.RawMessage) registry.Subject {
				if !at.Look {
					return registry.Subject{Unseen: true}
				}
				_, _, sites, err := b.rights(ctx, at.Project)
				if err != nil {
					return registry.Subject{Where: "on a page deck could not tell"}
				}
				b.mu.Lock()
				defer b.mu.Unlock()
				page, _ := b.current(ctx, readsAnySite(sites))
				if page == "" {
					return registry.Subject{Where: "on a page deck could not tell"}
				}
				return registry.Subject{Print: page, Where: "on " + untrusted.Cut(page, 300)}
			}
			c.Gate.Needs = func(_ context.Context, _ registry.Actor, s registry.Subject, _ json.RawMessage) ([]registry.Need, error) {
				return []registry.Need{{Right: manifest.Send, Subject: siteOf(s.Print)}}, nil
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

// rights are the declared rights of a project, the owner's answers and the sites it reads pages of in deck's browser:
// the hosts its programs hold the network of.
func (b *Browser) rights(ctx context.Context, project int64) (*manifest.Manifest, manifest.Grants, []string, error) {
	m, g, err := docs.Rights(ctx, b.Store.Q, project)
	if err != nil {
		return nil, nil, nil, err
	}
	return m, g, m.Held(g, manifest.Network, manifest.Programs), nil
}

// readsAnySite says a project holds the network of every site: it gets the browser without the owner's sign-ins.
func readsAnySite(sites []string) bool { return slices.Contains(sites, "*") }

// siteOf is the host of a page with the port its URL names, the subject of a right to act on it; empty for a page
// that has none.
func siteOf(page string) string {
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

// acts says a project holds the right to act on the site of a page without the owner: send on its host, granted
// always.
func acts(m *manifest.Manifest, g manifest.Grants, page string) bool {
	n := registry.Need{Right: manifest.Send, Subject: siteOf(page)}
	return n.Subject != "" && (m.Holds(g, n) || g.Gives(n))
}

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
	return egress.Reachable(domains, net.JoinHostPort(u.Hostname(), port))
}

func (b *Browser) reader(tool string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		var args struct{ URL, Action string }
		_ = json.Unmarshal(raw, &args)
		_, _, sites, err := b.rights(ctx, act.ProjectID)
		if err != nil {
			return nil, err
		}
		if tool == "browser_navigate" || tool == "browser_tabs" && args.Action == "new" && args.URL != "" {
			if !on(sites, args.URL) {
				return nil, registry.Refuse("%s is not a site this module holds the network of; it declares hosts under network in rights and the owner grants them (* is any site)", args.URL)
			}
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		res, err := b.call(ctx, readsAnySite(sites), tool, raw)
		if err != nil {
			return nil, fmt.Errorf("deck: %s: %v", tool, err)
		}
		if tool == "browser_tabs" && args.Action == "list" {
			ownTabs(res, sites)
		} else if tool != "browser_close" && tool != "browser_resize" {
			if page, off := b.offDomains(ctx, act.ProjectID); off {
				return nil, registry.Refuse("the browser shows %s, which is not a site this module holds the network of; what it shows is withheld", untrusted.Line(page, 300))
			}
		}
		return Content(b.Home, act.ProcessID, tool, res)
	}
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
	_, _, sites, err := b.rights(ctx, project)
	if err != nil {
		return "", true
	}
	page, perr := b.current(ctx, readsAnySite(sites))
	if perr != nil {
		return page, true
	}
	return page, !on(sites, page)
}

// Login opens a window of deck's browser at the page, for the owner to sign in with deck's profile.
func (b *Browser) Login(ctx context.Context, page string) error {
	if len(b.Argv) == 0 {
		return registry.Invalid("no browser is set up")
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

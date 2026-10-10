// Package signins keeps what deck reaches outside for projects: the sign-ins of deck's browser and of oauth2
// tools, and the MCP servers with the health of their tools.
package signins

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/tools"
	"deck2/internal/untrusted"
)

// Service keeps the connections; Ctx is the life of deck, for what finishes after a call.
type Service struct {
	St      *store.Store
	Ctx     context.Context
	Broker  *ask.Broker
	Browser *tools.Browser
	MCP     *tools.MCP
	OAuth   *tools.OAuth
}

func siteOf(page string) (string, error) {
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return "", registry.Invalid("deck: %q is not a web address", page)
	}
	return u.Hostname(), nil
}

func siteHealth(site string) string { return "site:" + site }

type browserLoginIn struct {
	URL string `desc:"Page to open, such as the site's sign-in page"`
}

// browserLogin opens a window of deck's browser for the owner to sign in to a site once.
func (c *Service) browserLogin(ctx context.Context, act registry.Actor, in browserLoginIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if err := c.Browser.Login(ctx, in.URL); err != nil {
		return nil, err
	}
	return map[string]any{"site": site, "text": "deck's browser shows " + in.URL + " in a window; sign in there, then call deck.browser_login_done"}, nil
}

type browserLoginDoneIn struct {
	URL string `desc:"A page of the site the owner signed in to"`
}

// browserLoginDone closes the window; processes that waited for this site go on.
func (c *Service) browserLoginDone(ctx context.Context, act registry.Actor, in browserLoginDoneIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	c.Browser.LoginDone()
	health.Write(ctx, c.St, health.Of{}, siteHealth(site), health.OK, "")
	n, err := c.signedIn(ctx, act.Workspace, "site", site)
	return map[string]any{"site": site, "resumed": n}, err
}

type browserNeedsLoginIn struct {
	URL string `desc:"The page that asked for a sign-in"`
	Why string `desc:"What the page showed"`
}

// browserNeedsLogin marks a site whose session ran out; the process waits until the owner signs in again.
func (c *Service) browserNeedsLogin(ctx context.Context, act registry.Actor, in browserNeedsLoginIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if act.ProcessID == 0 {
		return nil, registry.Invalid("deck: only a process waits for a sign-in")
	}
	health.Write(ctx, c.St, health.Of{}, siteHealth(site), health.NeedsLogin, untrusted.Cut(in.Why, 300))
	payload, _ := json.Marshal(map[string]string{"site": site})
	text := fmt.Sprintf("Sign in to %s in deck's browser (deck.browser_login), then say done: %s", site, untrusted.Cut(in.Why, 300))
	row, err := store.TxGet(c.St, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return ask.New(ctx, q, act.ProcessID, ask.KindSignIn, text, string(payload), time.Time{})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"waiting": row.ID, "text": "deck asked the owner to sign in to " + site +
		". Stop here and end your turn; deck resumes this session once the owner has signed in."}, nil
}

// signedIn answers the open sign-in asks for a site or a tool, so the processes behind them go on.
func (c *Service) signedIn(ctx context.Context, workspace int64, key, value string) (int, error) {
	open, err := c.St.Q.ListWorkspaceOpenAsks(ctx, workspace)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range open {
		var p map[string]string
		if q.Kind != ask.KindSignIn || json.Unmarshal([]byte(q.Payload), &p) != nil || p[key] != value {
			continue
		}
		if err := c.Broker.Answer(ctx, q.ID, "done"); err == nil {
			n++
		}
	}
	return n, nil
}

// Commands are the commands of connections.
func (c *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("browser_login", "Sign in to a site",
			"Opens a window of deck's browser at a page so the owner signs in once with deck's own profile.",
			registry.OwnerOnly, c.browserLogin),
		registry.Def("browser_login_done", "Signed in",
			"Closes the sign-in window of deck's browser; processes waiting for this site go on.",
			registry.OwnerOnly, c.browserLoginDone),
		registry.Def("browser_needs_login", "Site needs a sign-in",
			"Tells the owner that the session of a site ran out; the process waits until the owner signs in again.",
			[]string{registry.Agent}, c.browserNeedsLogin),
		registry.Def("oauth_start", "Sign in to a service",
			"Starts the owner's oauth2 sign-in for a tool in a window of deck's browser; it must finish within ten minutes.",
			registry.OwnerOnly, c.oauthStart),
	}
}

// ToolSignIn asks the owner once to sign in again to the service of a tool whose access ran out.
func (c *Service) ToolSignIn(ctx context.Context, p db.Project, tool, why string) {
	open, err := c.St.Q.ListWorkspaceOpenAsks(ctx, p.WorkspaceID)
	if err != nil {
		return
	}
	for _, q := range open {
		var pl map[string]string
		if q.Kind == ask.KindSignIn && json.Unmarshal([]byte(q.Payload), &pl) == nil && pl["tool"] == tool {
			return
		}
	}
	payload, _ := json.Marshal(map[string]string{"tool": tool})
	text := fmt.Sprintf("%s needs you to sign in to its service again (deck.oauth_start): %s", tool, untrusted.Cut(why, 300))
	err = c.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.ForProject(ctx, q, p.WorkspaceID, p.ID, 0, ask.KindSignIn, text, string(payload), time.Time{})
		return err
	})
	if err != nil {
		health.Write(ctx, c.St, health.Project(p), "tool:"+tool, health.NeedsLogin, text+"; deck could not ask: "+err.Error())
	}
}

type oauthStartIn struct {
	Tool string `desc:"The tool, as project.tool"`
}

// oauthStart begins the owner's sign-in to the service of an oauth2 tool in a window of deck's browser;
// the service calls deck back on localhost within ten minutes.
func (c *Service) oauthStart(ctx context.Context, act registry.Actor, in oauthStartIn) (any, error) {
	project, local, ok := strings.Cut(in.Tool, ".")
	if !ok {
		return nil, registry.Invalid("deck: name the tool as project.tool")
	}
	p, err := access.Project(ctx, c.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, c.St.Q, p)
	if err != nil {
		return nil, err
	}
	var auth tools.Auth
	for _, t := range m.Tools.HTTP {
		if t.Name == local {
			_ = json.Unmarshal(t.Auth, &auth)
		}
	}
	if auth.Type != "oauth2" {
		return nil, registry.Invalid("deck: %s is not an oauth2 tool of %s", local, p.Name)
	}
	var mu sync.Mutex
	opened, finished := false, false
	link, err := c.OAuth.Start(ctx, p.WorkspaceID, auth, func(err error) {
		mu.Lock()
		finished = true
		close := opened
		mu.Unlock()
		if close {
			c.Browser.LoginDone()
		}
		if err != nil {
			c.ToolHealth(c.Ctx, p, in.Tool, "needs_login", err.Error())
			return
		}
		c.ToolHealth(c.Ctx, p, in.Tool, "ok", "")
		_, _ = c.signedIn(c.Ctx, p.WorkspaceID, "tool", in.Tool)
	})
	if err != nil {
		return nil, err
	}
	window := c.Browser.Login(ctx, link) == nil
	mu.Lock()
	opened = window
	late := window && finished
	mu.Unlock()
	if late {
		c.Browser.LoginDone()
	}
	return map[string]any{"url": link, "window": window}, nil
}

// ServerStatus records what became of a server start: its tools and health, or the error.
func (c *Service) ServerStatus(key string, list []*mcp.Tool, err error) {
	ctx := c.Ctx
	subject, of := key, health.Of{}
	if key == tools.BrowserKey && err == nil {
		c.Browser.Keep(list)
	}
	if project, server, ok := tools.ParseKey(key); ok {
		p, perr := c.St.Q.GetProject(ctx, project)
		if perr != nil {
			return
		}
		subject, of = "mcp:"+p.Name+"."+server, health.Project(p)
		if err == nil {
			if kerr := c.MCP.Keep(ctx, project, server, list); kerr != nil {
				err = fmt.Errorf("keep its tools: %w", kerr)
			}
		}
	}
	if err != nil {
		health.Write(ctx, c.St, of, subject, health.Error, err.Error())
		return
	}
	health.Write(ctx, c.St, of, subject, health.OK, "")
}

// ToolHealth is the health of a tool of a project.
func (c *Service) ToolHealth(ctx context.Context, p db.Project, tool, status, text string) {
	health.Write(ctx, c.St, health.Project(p), "tool:"+tool, status, text)
}

// Servers starts the MCP servers of a project whose tools deck has not listed yet, so a process that starts
// now sees them; the servers themselves run from the first call.
func (c *Service) Servers(ctx context.Context, projectID int64) {
	p, err := c.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	m, err := docs.For(ctx, c.St.Q, registry.Actor{}, p.ID)
	if err != nil {
		return
	}
	c.MCP.Start(ctx, p, m)
	if len(m.Domains.Read) > 0 || len(m.Domains.Act) > 0 {
		c.Browser.Ensure(ctx)
	}
}

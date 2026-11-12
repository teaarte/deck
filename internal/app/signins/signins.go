// Package signins keeps what deck reaches outside for projects: the sign-ins of deck's browser, of oauth2 tools and
// of the CLI agents, and the MCP servers with the health of their tools.
package signins

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/tools"
	"deck2/internal/untrusted"
)

// Service keeps the sign-ins; Ctx is the life of deck, for what finishes after a call.
type Service struct {
	St      *store.Store
	Ctx     context.Context
	Broker  *ask.Broker
	Browser *tools.Browser
	MCP     *tools.MCP
	OAuth   *tools.OAuth
	// Agent is a built-in provider by name, nil for none; Check runs its self-check again.
	Agent func(name string) provider.Provider
	Check func(name string)
	// Emit tells the clients a topic changed that no write of the store announced, such as a sign-in from deck.
	Emit func(events.Event)
	// Go runs work beside the calls; false once deck stops.
	Go func(func(context.Context)) bool

	mu sync.Mutex
	// logins are the sign-ins of CLI agents from deck, by provider.
	logins map[string]*signIn
}

func siteOf(page string) (string, error) {
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return "", registry.Invalid("%q is not a web address", page)
	}
	return u.Hostname(), nil
}

type browserLoginIn struct {
	URL string `desc:"Page to open, such as the site's sign-in page"`
}

// browserLogin opens a window of deck's browser for the owner to sign in to a site once.
func (s *Service) browserLogin(ctx context.Context, act registry.Actor, in browserLoginIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if err := s.Browser.Login(ctx, in.URL); err != nil {
		return nil, err
	}
	return map[string]any{"site": site, "text": "deck's browser shows " + in.URL + " in a window; sign in there, then call deck.browser_login_finish"}, nil
}

type browserLoginFinishIn struct {
	URL string `desc:"A page of the site the owner signed in to"`
}

// browserLoginFinish closes the window; processes that waited for this site go on.
func (s *Service) browserLoginFinish(ctx context.Context, act registry.Actor, in browserLoginFinishIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	s.Browser.LoginDone()
	if err := health.Write(ctx, s.St, health.Of{}, health.Site(site), health.OK, ""); err != nil {
		return nil, err
	}
	n, err := s.signedIn(ctx, act.Workspace, ask.SignIn{Site: site})
	return map[string]any{"site": site, "resumed": n}, err
}

type browserLoginRequestIn struct {
	URL string `desc:"The page that asked for a sign-in"`
	Why string `desc:"What the page showed"`
}

// browserLoginRequest marks a site whose session ran out; the process waits until the owner signs in again.
func (s *Service) browserLoginRequest(ctx context.Context, act registry.Actor, in browserLoginRequestIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if act.ProcessID == 0 {
		return nil, registry.Invalid("only a process waits for a sign-in")
	}
	if err := health.Write(ctx, s.St, health.Of{}, health.Site(site), health.NeedsLogin, untrusted.Cut(in.Why, 300)); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(ask.SignIn{Site: site})
	text := fmt.Sprintf("Sign in to %s in deck's browser (deck.browser_login), then say done: %s", site, untrusted.Cut(in.Why, 300))
	row, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return ask.New(ctx, q, act.ProcessID, ask.KindSignIn, text, string(payload), time.Time{})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"waiting": row.ID, "text": "deck asked the owner to sign in to " + site +
		". Stop here and end your turn; deck resumes this session once the owner has signed in."}, nil
}

// signedIn answers the open sign-in asks for a site or a tool, so the processes behind them go on.
func (s *Service) signedIn(ctx context.Context, workspace int64, to ask.SignIn) (int, error) {
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, workspace)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range open {
		if q.Kind != ask.KindSignIn {
			continue
		}
		if p, err := ask.Decode[ask.SignIn](q.Payload); err != nil || p.Provider || p.Site != to.Site || p.Tool != to.Tool {
			continue
		}
		if err := s.Broker.Answer(ctx, q.ID, "done"); err == nil {
			n++
		}
	}
	return n, nil
}

// Commands are the commands of sign-ins: of deck's browser, of oauth2 tools and of CLI agents.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.siteCommands(), s.agentLoginCommands())
}

func (s *Service) siteCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("browser_login", "Sign in to a site",
			"Opens a window of deck's browser at a page so the owner signs in once with deck's own profile.",
			registry.OwnerOnly, s.browserLogin).Stays(),
		registry.Def("browser_login_finish", "Signed in",
			"Closes the sign-in window of deck's browser; processes waiting for this site go on.",
			registry.OwnerOnly, s.browserLoginFinish).Stays(),
		registry.Def("browser_login_request", "Site needs a sign-in",
			"Tells the owner that the session of a site ran out; the process waits until the owner signs in again.",
			[]string{registry.Agent}, s.browserLoginRequest).Stays(),
		registry.Def("oauth_start", "Sign in to a service",
			"Starts the owner's oauth2 sign-in for a tool in a window of deck's browser; it must finish within ten minutes.",
			registry.OwnerOnly, s.oauthStart).Stays(),
	}
}

// ToolSignIn asks the owner once to sign in again to the service of a tool whose access ran out.
func (s *Service) ToolSignIn(ctx context.Context, p db.Project, tool, why string) {
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, p.WorkspaceID)
	if err != nil {
		return
	}
	for _, q := range open {
		if q.Kind != ask.KindSignIn {
			continue
		}
		if pl, err := ask.Decode[ask.SignIn](q.Payload); err == nil && pl.Tool == tool {
			return
		}
	}
	payload, _ := json.Marshal(ask.SignIn{Tool: tool})
	text := fmt.Sprintf("%s needs you to sign in to its service again (deck.oauth_start): %s", tool, untrusted.Cut(why, 300))
	if a, _, err := s.auth(ctx, p, strings.TrimPrefix(tool, p.Name+".")); err == nil && a.Type == "oauth2" {
		text += fmt.Sprintf("; the sign-in is at %s, its token from %s", host(a.AuthURL), host(a.TokenURL))
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.ForProject(ctx, q, p.WorkspaceID, p.ID, 0, ask.KindSignIn, text, string(payload), time.Time{})
		return err
	})
	if err != nil {
		if err := health.Write(ctx, s.St, health.Project(p), health.Tool(tool), health.NeedsLogin, text+"; deck could not ask: "+err.Error()); err != nil {
			slog.Error("write health", "err", err)
		}
	}
}

// auth is the sign-in of a tool of the project's manifest, and the hosts its programs hold the network of.
func (s *Service) auth(ctx context.Context, p db.Project, tool string) (manifest.Auth, []string, error) {
	var auth manifest.Auth
	m, g, err := docs.Rights(ctx, s.St.Q, p.ID)
	if err != nil {
		return auth, nil, err
	}
	for _, t := range m.Tools.HTTP {
		if t.Name == tool {
			auth = t.AuthOf()
		}
	}
	return auth, m.Held(g, manifest.Network, manifest.Programs), nil
}

// host is the host of an address, or the address when it has none.
func host(address string) string {
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		return u.Host
	}
	return address
}

type oauthStartIn struct {
	Tool string `desc:"The tool, as project.tool"`
}

// oauthStart begins the owner's sign-in to the service of an oauth2 tool in a window of deck's browser;
// the service calls deck back on localhost within ten minutes.
func (s *Service) oauthStart(ctx context.Context, act registry.Actor, in oauthStartIn) (any, error) {
	project, local, ok := strings.Cut(in.Tool, ".")
	if !ok {
		return nil, registry.Invalid("name the tool as project.tool")
	}
	p, err := access.Project(ctx, s.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	auth, domains, err := s.auth(ctx, p, local)
	if err != nil {
		return nil, err
	}
	if auth.Type != "oauth2" {
		return nil, registry.Invalid("%s is not an oauth2 tool of %s", local, p.Name)
	}
	var mu sync.Mutex
	opened, finished := false, false
	link, err := s.OAuth.Start(ctx, p, domains, auth, func(err error) {
		mu.Lock()
		finished = true
		close := opened
		mu.Unlock()
		if close {
			s.Browser.LoginDone()
		}
		if err != nil {
			s.ToolHealth(s.Ctx, p, in.Tool, "needs_login", err.Error())
			return
		}
		s.ToolHealth(s.Ctx, p, in.Tool, "ok", "")
		_, _ = s.signedIn(s.Ctx, p.WorkspaceID, ask.SignIn{Tool: in.Tool})
	})
	if err != nil {
		return nil, err
	}
	window := s.Browser.Login(ctx, link) == nil
	mu.Lock()
	opened = window
	late := window && finished
	mu.Unlock()
	if late {
		s.Browser.LoginDone()
	}
	return map[string]any{"url": link, "window": window}, nil
}

// ServerStatus records what became of a server start: its tools and health, or the error.
func (s *Service) ServerStatus(key string, list []*mcp.Tool, err error) {
	ctx := s.Ctx
	subject, of := key, health.Of{}
	if key == tools.BrowserKey && err == nil {
		s.Browser.Keep(list)
	}
	if project, server, ok := tools.ParseKey(key); ok {
		p, perr := s.St.Q.GetProject(ctx, project)
		if perr != nil {
			return
		}
		subject, of = health.MCP(p.Name, server), health.Project(p)
		if err == nil {
			if kerr := s.MCP.Keep(ctx, project, server, list); kerr != nil {
				err = fmt.Errorf("keep its tools: %w", kerr)
			}
		}
	}
	if err != nil {
		if err := health.Write(ctx, s.St, of, subject, health.Error, err.Error()); err != nil {
			slog.Error("write health", "err", err)
		}
		return
	}
	if err := health.Write(ctx, s.St, of, subject, health.OK, ""); err != nil {
		slog.Error("write health", "err", err)
	}
}

// ToolHealth is the health of a tool of a project.
func (s *Service) ToolHealth(ctx context.Context, p db.Project, tool, status, text string) {
	if err := health.Write(ctx, s.St, health.Project(p), health.Tool(tool), status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}

// Servers starts the MCP servers of a project whose tools deck has not listed yet, so a process that starts
// now sees them; the servers themselves run from the first call.
func (s *Service) Servers(ctx context.Context, projectID int64) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	m, g, err := docs.Rights(ctx, s.St.Q, p.ID)
	if err != nil {
		return
	}
	s.MCP.Start(ctx, p, m)
	if len(m.Held(g, manifest.Network, manifest.Programs)) > 0 {
		s.Browser.Ensure(ctx)
	}
}

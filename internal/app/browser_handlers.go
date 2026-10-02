package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// DefaultBrowser starts Playwright MCP of a known version, so the tools deck sorts into reading and acting
// stay the ones it knows.
var DefaultBrowser = []string{"npm", "exec", "--yes", "--package=@playwright/mcp@0.0.82", "--", "playwright-mcp"}

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
func (a *App) browserLogin(ctx context.Context, act registry.Actor, in browserLoginIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if err := a.Browser.Login(ctx, in.URL); err != nil {
		return nil, err
	}
	return map[string]any{"site": site, "text": "deck's browser shows " + in.URL + " in a window; sign in there, then call deck.browser_login_done"}, nil
}

type browserLoginDoneIn struct {
	URL string `desc:"A page of the site the owner signed in to"`
}

// browserLoginDone closes the window; processes that waited for this site go on.
func (a *App) browserLoginDone(ctx context.Context, act registry.Actor, in browserLoginDoneIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	a.Browser.LoginDone()
	a.setHealth(ctx, siteHealth(site), "ok", "")
	n, err := a.signedIn(ctx, act.Workspace, "site", site)
	return map[string]any{"site": site, "resumed": n}, err
}

type browserNeedsLoginIn struct {
	URL string `desc:"The page that asked for a sign-in"`
	Why string `desc:"What the page showed"`
}

// browserNeedsLogin marks a site whose session ran out; the process waits until the owner signs in again.
func (a *App) browserNeedsLogin(ctx context.Context, act registry.Actor, in browserNeedsLoginIn) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if act.ProcessID == 0 {
		return nil, registry.Invalid("deck: only a process waits for a sign-in")
	}
	a.setHealth(ctx, siteHealth(site), "needs_login", untrusted.Cut(in.Why, 300))
	payload, _ := json.Marshal(map[string]string{"site": site})
	text := fmt.Sprintf("Sign in to %s in deck's browser (deck.browser_login), then say done: %s", site, untrusted.Cut(in.Why, 300))
	row, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return ask.New(ctx, q, act.ProcessID, ask.KindSignIn, text, string(payload), time.Time{})
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("asks")
	return map[string]any{"waiting": row.ID, "text": "deck asked the owner to sign in to " + site +
		". Stop here and end your turn; deck resumes this session once the owner has signed in."}, nil
}

// signedIn answers the open sign-in asks for a site or a tool, so the processes behind them go on.
func (a *App) signedIn(ctx context.Context, workspace int64, key, value string) (int, error) {
	open, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, workspace)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range open {
		var p map[string]string
		if q.Kind != ask.KindSignIn || json.Unmarshal([]byte(q.Payload), &p) != nil || p[key] != value {
			continue
		}
		if err := a.Asks.Answer(ctx, q.ID, "done"); err == nil {
			n++
		}
	}
	return n, nil
}

func (a *App) browserCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("browser_login", "Sign in to a site",
			"Opens a window of deck's browser at a page so the owner signs in once with deck's own profile.",
			registry.OwnerOnly, a.browserLogin),
		registry.Def("browser_login_done", "Signed in",
			"Closes the sign-in window of deck's browser; processes waiting for this site go on.",
			registry.OwnerOnly, a.browserLoginDone),
		registry.Def("browser_needs_login", "Site needs a sign-in",
			"Tells the owner that the session of a site ran out; the process waits until the owner signs in again.",
			[]string{registry.Agent}, a.browserNeedsLogin),
	}
}

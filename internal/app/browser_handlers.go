package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"deck2/internal/untrusted"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store/db"
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

// browserLogin opens a window of deck's browser for the owner to sign in to a site once.
func (a *App) browserLogin(ctx context.Context, act registry.Actor, in struct{ URL string }) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	if err := a.Browser.Login(ctx, in.URL); err != nil {
		return nil, err
	}
	return map[string]any{"site": site, "text": "deck's browser shows " + in.URL + " in a window; sign in there, then call deck.browser_login_done"}, nil
}

// browserLoginDone closes the window; processes that waited for this site go on.
func (a *App) browserLoginDone(ctx context.Context, act registry.Actor, in struct{ URL string }) (any, error) {
	site, err := siteOf(in.URL)
	if err != nil {
		return nil, err
	}
	a.Browser.LoginDone()
	a.setHealth(ctx, siteHealth(site), "ok", "")
	n, err := a.signedIn(ctx, act.Workspace, "site", site)
	return map[string]any{"site": site, "resumed": n}, err
}

// browserNeedsLogin marks a site whose session ran out; the process waits until the owner signs in again.
func (a *App) browserNeedsLogin(ctx context.Context, act registry.Actor, in struct{ URL, Why string }) (any, error) {
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
	var row db.Ask
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		row, err = q.CreateAsk(ctx, db.CreateAskParams{ProcessID: act.ProcessID, Kind: ask.KindSignIn, Text: text, Payload: string(payload)})
		return err
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

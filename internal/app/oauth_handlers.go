package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/tools"
	"deck2/internal/untrusted"
)

// toolSignIn asks the owner once to sign in again to the service of a tool whose access ran out.
func (a *App) toolSignIn(ctx context.Context, p db.Project, tool, why string) {
	open, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, p.WorkspaceID)
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
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.ForProject(ctx, q, p.WorkspaceID, p.ID, 0, ask.KindSignIn, text, string(payload), time.Time{})
		return err
	})
	if err == nil {
		a.Bus.Publish("asks")
	}
}

type oauthStartIn struct {
	Tool string `desc:"The tool, as project.tool"`
}

// oauthStart begins the owner's sign-in to the service of an oauth2 tool in a window of deck's browser;
// the service calls deck back on localhost within ten minutes.
func (a *App) oauthStart(ctx context.Context, act registry.Actor, in oauthStartIn) (any, error) {
	project, local, ok := strings.Cut(in.Tool, ".")
	if !ok {
		return nil, registry.Invalid("deck: name the tool as project.tool")
	}
	p, err := a.project(ctx, act, project)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
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
	link, err := a.Tools.OAuth.Start(ctx, p.WorkspaceID, auth, func(err error) {
		mu.Lock()
		finished = true
		close := opened
		mu.Unlock()
		if close {
			a.Browser.LoginDone()
		}
		if err != nil {
			a.toolHealth(a.ctx, p, in.Tool, "needs_login", err.Error())
			return
		}
		a.toolHealth(a.ctx, p, in.Tool, "ok", "")
		_, _ = a.signedIn(a.ctx, p.WorkspaceID, "tool", in.Tool)
	})
	if err != nil {
		return nil, err
	}
	window := a.Browser.Login(ctx, link) == nil
	mu.Lock()
	opened = window
	late := window && finished
	mu.Unlock()
	if late {
		a.Browser.LoginDone()
	}
	return map[string]any{"url": link, "window": window}, nil
}

func (a *App) oauthCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("oauth_start", "Sign in to a service",
			"Starts the owner's oauth2 sign-in for a tool in a window of deck's browser; it must finish within ten minutes.",
			registry.OwnerOnly, a.oauthStart),
	}
}

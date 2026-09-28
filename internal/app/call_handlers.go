package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// decide answers a frozen call or an unknown outcome: allowing or retrying makes the call now and delivers
// what it gave to the process that asked; refusing or counting it done only tells the process.
func (a *App) decide(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	run := q.Kind == ask.KindCall && (answer == "allow" || answer == "allow_always") || q.Kind == ask.KindUnknown && answer == ask.UnknownRetry
	refuse := q.Kind == ask.KindCall && (answer == "deny" || strings.HasPrefix(answer, "deny:")) || q.Kind == ask.KindUnknown && answer == ask.UnknownDone
	switch {
	case run:
		if q.State != "open" {
			return nil, notOpen
		}
		var then func() error
		if answer == "allow_always" {
			then = func() error { return a.allowAlways(ctx, act, q) }
		}
		out, err := a.Gateway.Decide(ctx, q, answer, then)
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, notOpen
		}
		a.lateAnswer(ctx, q)
		if err != nil {
			return nil, err
		}
		a.linkTriggeredCard(ctx, q, out)
		return map[string]any{"ok": true, "result": out}, nil
	case refuse:
		if err := a.Asks.Answer(ctx, q.ID, answer); err != nil {
			if errors.Is(err, ask.ErrNotOpen) {
				return nil, notOpen
			}
			return nil, err
		}
		if q.Kind == ask.KindCall {
			a.Gateway.Record(ctx, q, "rejected", "the owner said no")
		}
		return map[string]any{"ok": true}, nil
	case q.Kind == ask.KindUnknown:
		return nil, registry.Invalid("deck: answer an unknown outcome with %s", strings.Join(ask.UnknownAnswers, " or "))
	}
	return nil, registry.Invalid("deck: answer a confirmation with allow, allow_always, deny or deny:<reason>")
}

// linkTriggeredCard links a card to the process a lane trigger started once the owner allowed it, as
// the lane trigger does when the call runs at once.
func (a *App) linkTriggeredCard(ctx context.Context, asked db.Ask, out any) {
	started, ok := out.(Started)
	var f gateway.Frozen
	if !ok || json.Unmarshal([]byte(asked.Payload), &f) != nil || f.Caller.Kind != registry.Trigger {
		return
	}
	var in struct{ Card int64 }
	if json.Unmarshal(f.Call.Input, &in) != nil || in.Card == 0 {
		return
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := q.GetCard(ctx, in.Card)
		if err != nil || c.ProjectID != asked.ProjectID {
			return err
		}
		return q.SetCardProcess(ctx, db.SetCardProcessParams{ProcessID: sql.NullInt64{Int64: started.Process, Valid: true}, ID: in.Card})
	})
	if err == nil {
		a.Bus.Publish("board")
	}
}

var notOpen = registry.Conflict("deck: this ask is already answered or expired")

// allowAlways adds the tool or command of a frozen call to the allowed external list of its project, or the
// site of a browser action to its act domains, as the owner's own manifest change.
func (a *App) allowAlways(ctx context.Context, act registry.Actor, q db.Ask) error {
	var f gateway.Frozen
	if err := json.Unmarshal([]byte(q.Payload), &f); err != nil {
		return err
	}
	p, err := a.Store.Q.GetProject(ctx, q.ProjectID)
	if err != nil {
		return registry.Invalid("deck: only a tool or command of a project can be allowed always")
	}
	local, ok := strings.CutPrefix(f.Call.Command, p.Name+".")
	if strings.HasPrefix(f.Call.Command, registry.Builtin+".browser_") {
		u, err := url.Parse(f.Tool)
		if err != nil || u.Host == "" {
			return registry.Invalid("deck: deck could not tell the page of this browser action; allow it once or add the site to domains.act")
		}
		return a.addToList(ctx, act, p, u.Host, "domains", "act")
	}
	if !ok {
		return registry.Invalid("deck: only a tool or command of the project can be allowed always")
	}
	if c, err := a.Registry.Lookup(ctx, p.WorkspaceID, f.Call.Command); err == nil && c.Class != registry.External {
		return registry.Invalid("deck: %s is %s: it follows the autonomy of the project, not the allowed external list", c.Name, c.Class)
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return err
	}
	if server, tool, ok := strings.Cut(local, "."); ok {
		err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.ApproveMCPTool(ctx, db.ApproveMCPToolParams{ProjectID: p.ID, Server: server, Name: tool})
		})
		if err != nil {
			return err
		}
	}
	if slices.Contains(m.AllowedExternal, local) {
		return nil
	}
	return a.addToList(ctx, act, p, local, "allowed_external")
}

// addToList adds a value to a list of the project's manifest at the path of keys, as a new revision.
func (a *App) addToList(ctx context.Context, act registry.Actor, p db.Project, value string, path ...string) error {
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return err
	}
	raw, err := docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindManifest, docs.ManifestName)
	if err != nil {
		return err
	}
	var doc map[string]any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return err
	}
	at := doc
	for _, k := range path[:len(path)-1] {
		next, _ := at[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
			at[k] = next
		}
		at = next
	}
	last := path[len(path)-1]
	list, _ := at[last].([]any)
	if slices.Contains(list, any(value)) {
		return nil
	}
	at[last] = append(list, value)
	b, _ := json.MarshalIndent(doc, "", "  ")
	_, err = a.SaveManifest(ctx, act, p, b, "allow "+value+" always", 0, m.Revision, Call{})
	return err
}

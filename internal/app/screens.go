package app

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

//go:embed screens.json
var deckScreensJSON []byte

// deckScreens are the screens deck ships: the cockpit, providers and the page of a process.
var deckScreens = func() (d struct {
	Menu    []string          `json:"menu"`
	Screens []manifest.Screen `json:"screens"`
}) {
	if err := json.Unmarshal(deckScreensJSON, &d); err != nil {
		panic(err)
	}
	if err := manifest.CheckDeckScreens(d.Screens); err != nil {
		panic(err)
	}
	return d
}()

func deckScreen(name string) *manifest.Screen {
	for i := range deckScreens.Screens {
		if deckScreens.Screens[i].Name == name {
			return &deckScreens.Screens[i]
		}
	}
	return nil
}

type menuEntry struct {
	Project string       `json:"project,omitempty"`
	Title   string       `json:"title"`
	Screens []screenLink `json:"screens"`
}

type screenLink struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// menu is the navigation of the client: deck's screens, then each project's menu, or all its screens without one.
func (a *App) menu(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	out := []menuEntry{{Title: "deck"}}
	for _, name := range deckScreens.Menu {
		out[0].Screens = append(out[0].Screens, screenLink{Name: name, Title: deckScreen(name).Title})
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			continue
		}
		e := menuEntry{Project: p.Name, Title: p.Name, Screens: []screenLink{}}
		names := m.Menu
		if len(names) == 0 {
			for _, s := range m.Screens {
				if s.Record == "" {
					names = append(names, s.Name)
				}
			}
		}
		for _, n := range names {
			e.Screens = append(e.Screens, screenLink{Name: n, Title: m.Screen(n).Title})
		}
		out = append(out, e)
	}
	return out, nil
}

// screenGet resolves a screen of a project, or of deck without one, into A2UI messages.
func (a *App) screenGet(ctx context.Context, act registry.Actor, in struct {
	Project, Screen string
	Record          int64
	Topic           string
}) (any, error) {
	req := screen.Request{Record: in.Record, Topic: in.Topic}
	if in.Project == "" {
		req.Screen = deckScreen(in.Screen)
		req.Project.WorkspaceID = act.Workspace
	} else {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			return nil, err
		}
		req.Project, req.Manifest, req.Screen = p, m, m.Screen(in.Screen)
	}
	if req.Screen == nil {
		return nil, registry.NotFound("deck: no screen %q", in.Screen)
	}
	out, err := screen.Render(ctx, a.Store, sources{a}, req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	return out, nil
}

type sources struct{ a *App }

func (s sources) Items(ctx context.Context, from string, sc screen.Scope) ([]map[string]any, error) {
	names, err := s.projectNames(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	switch from {
	case "asks":
		return s.a.askItems(ctx, sc, names)
	case "processes":
		return s.processItems(ctx, sc, names)
	case "unattended":
		rows, err := s.a.Store.Q.ListUnattended(ctx, db.ListUnattendedParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, Lim: 50})
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			canUndo := r.Command != "" && !r.UndoneAt.Valid
			it := map[string]any{"id": r.ID, "project": names[r.ProjectID], "author": r.Author, "text": r.Text,
				"can_undo": canUndo, "undone": r.UndoneAt.Valid, "created_at": r.CreatedAt}
			if canUndo {
				it["_actions"] = []map[string]any{call("undo", false, "deck.unattended_undo", map[string]any{"entry": r.ID})}
			}
			out = append(out, it)
		}
		return out, nil
	case "providers":
		return s.providerItems(ctx, sc)
	case "log":
		return s.logItems(sc.Record)
	}
	return nil, fmt.Errorf("unknown source %q", from)
}

func (s sources) projectNames(ctx context.Context, workspace int64) (map[int64]string, error) {
	ps, err := s.a.Store.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, p := range ps {
		out[p.ID] = p.Name
	}
	return out, nil
}

// call is a button that calls a command; verb names its label for the client.
func call(verb string, primary bool, command string, params map[string]any) map[string]any {
	out := map[string]any{"verb": verb, "command": command, "params": params}
	if primary {
		out["primary"] = true
	}
	return out
}

func (s sources) processItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	var ps []db.Process
	if sc.Record != 0 {
		p, err := s.a.Store.Q.GetProcess(ctx, sc.Record)
		if err == nil && names[p.ProjectID] != "" && (sc.Project == 0 || p.ProjectID == sc.Project) {
			ps = append(ps, p)
		}
	} else {
		open, err := s.a.Store.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project})
		if err != nil {
			return nil, err
		}
		recent, err := s.a.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, State: "", Lim: 50})
		if err != nil {
			return nil, err
		}
		ps = open
		for _, p := range recent {
			if !slices.ContainsFunc(open, func(o db.Process) bool { return o.ID == p.ID }) {
				ps = append(ps, p)
			}
		}
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		v := procView(p)
		it := map[string]any{"id": p.ID, "project": names[p.ProjectID], "command": p.Command, "kind": p.Kind, "state": p.State,
			"silent": p.SilentSinceMs.Valid, "started_at": p.StartedAt, "provider": p.Provider, "error": v.Error}
		acts := []map[string]any{{"verb": "open", "screen": "process", "record": p.ID}}
		if !slices.Contains([]string{process.StateDone, process.StateError, process.StateInterrupted, process.StateStopped}, p.State) {
			acts = append(acts, call("stop", false, "deck.process_stop", map[string]any{"process": p.ID}))
		}
		if sc.Record != 0 {
			acts = acts[1:]
		}
		it["_actions"] = acts
		out = append(out, it)
	}
	return out, nil
}

func (s sources) providerItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.a.providersList(ctx, registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace}, struct{ Command string }{})
	if err != nil {
		return nil, err
	}
	views := got.(map[string]any)["providers"].([]providerView)
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		caps := make([]string, 0, len(v.Caps))
		for _, c := range v.Caps {
			caps = append(caps, string(c))
		}
		out = append(out, map[string]any{"id": v.Name, "name": v.Name, "agent": v.Agent, "model": v.Model, "url": v.URL, "status": v.Status,
			"verdict": v.Verdict, "speed": v.Speed, "version": v.Version, "error": v.Error, "caps": strings.Join(caps, ", "),
			"_actions": []map[string]any{call("check", false, "deck.provider_check", map[string]any{"provider": v.Name})}})
	}
	return out, nil
}

// logItems are the last lines of the log of a process while it is open.
func (s sources) logItems(processID int64) ([]map[string]any, error) {
	out := []map[string]any{}
	f, err := os.Open(s.a.Runner.LogPath(processID))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if out = append(out, line); len(out) > 200 {
			out = out[1:]
		}
	}
	return out, sc.Err()
}

// askItems are what waits for the owner, each with the buttons its kind answers with; confirmations that
// expired stay until the owner clears them.
func (a *App) askItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	open, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := a.Store.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: sc.Workspace,
		Now: store.Time(a.Cfg.Now())})
	if err != nil {
		return nil, err
	}
	all := append(open, expired...)
	slices.SortFunc(all, func(x, y db.Ask) int { return int(x.ID - y.ID) })
	out := make([]map[string]any, 0, len(all))
	for _, q := range all {
		if sc.Project != 0 && q.ProjectID != sc.Project {
			continue
		}
		out = append(out, a.askItem(ctx, q, names))
	}
	return out, nil
}

func (a *App) askItem(ctx context.Context, q db.Ask, names map[int64]string) map[string]any {
	answer := func(verb string, primary bool, value string) map[string]any {
		return call(verb, primary, "deck.ask_answer", map[string]any{"ask": q.ID, "answer": value})
	}
	it := map[string]any{"id": q.ID, "kind": q.Kind, "project": names[q.ProjectID], "text": q.Text, "what": firstLine(q.Text),
		"who": "", "where": "", "created_at": q.CreatedAt, "expires_at": q.ExpiresAt.String, "expired": q.State == "expired"}
	if q.ProcessID.Valid {
		it["who"] = a.processName(ctx, q.ProcessID.Int64)
	}
	var acts []map[string]any
	deny := []map[string]any{answer("deny", false, "deny"), withInput(answer("deny_because", false, "deny"), "reason")}
	switch q.Kind {
	case ask.KindQuestion:
		var pl struct {
			Questions json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		if len(pl.Questions) > 0 {
			it["_questions"] = pl.Questions
		}
		acts = []map[string]any{withInput(answer("answer", true, ""), "answer")}
	case ask.KindCall, ask.KindUnknown:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		it["who"] = a.callerName(ctx, f.Caller)
		it["what"], it["where"] = f.Call.Command, f.Target
		if c, err := a.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			it["what"] = c.Title + " (" + f.Call.Command + ")"
			if f.Target == "" && c.Target != nil {
				it["where"] = c.Target(f.Tool)
			}
		}
		details, _ := json.MarshalIndent(map[string]any{"command": f.Call.Command, "input": f.Call.Input, "caller": f.Caller,
			"target": f.Target, "tool": f.Tool}, "", "  ")
		it["_details"] = string(details)
		if q.Kind == ask.KindUnknown {
			acts = []map[string]any{answer("retry", true, ask.UnknownRetry), answer("mark_done", false, ask.UnknownDone)}
			break
		}
		acts = append([]map[string]any{until(answer("allow", true, "allow"), q), until(answer("allow_always", false, "allow_always"), q)}, deny...)
	case ask.KindConfirm, ask.KindGrant:
		it["_details"] = pretty(q.Payload)
		if q.Kind == ask.KindConfirm {
			var pl struct {
				Tool  string         `json:"tool"`
				Input map[string]any `json:"input"`
			}
			if json.Unmarshal([]byte(q.Payload), &pl) == nil && pl.Tool != "" {
				it["what"], it["where"] = pl.Tool, confirmTarget(pl.Input)
			}
		}
		acts = append([]map[string]any{until(answer("allow", true, "allow"), q)}, deny...)
	case ask.KindLogin:
		acts = []map[string]any{answer("retry", true, ask.LoginRetry), answer("new_session", false, ask.LoginNewSession)}
	case ask.KindLimit:
		acts = []map[string]any{answer("continue", true, ask.LimitContinue)}
	case ask.KindSignIn:
		var pl map[string]string
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		if site := pl["site"]; site != "" {
			it["where"] = site
			acts = []map[string]any{call("sign_in", true, "deck.browser_login", map[string]any{"url": "https://" + site}),
				call("signed_in", false, "deck.browser_login_done", map[string]any{"url": "https://" + site})}
		} else {
			it["where"] = pl["tool"]
			acts = []map[string]any{call("sign_in", true, "deck.oauth_start", map[string]any{"tool": pl["tool"]})}
		}
	case ask.KindWarning:
		acts = []map[string]any{answer("dismiss", true, "ok")}
	}
	if q.State == "expired" {
		acts = []map[string]any{answer("deny", true, "deny")}
	}
	it["_actions"] = acts
	return it
}

func withInput(act map[string]any, param string) map[string]any {
	act["input"] = param
	return act
}

// until hides a button once its confirmation expires.
func until(act map[string]any, q db.Ask) map[string]any {
	if q.ExpiresAt.Valid {
		act["until"] = q.ExpiresAt.String
	}
	return act
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func pretty(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func (a *App) processName(ctx context.Context, id int64) string {
	p, err := a.Store.Q.GetProcess(ctx, id)
	if err != nil {
		return fmt.Sprintf("process #%d", id)
	}
	return fmt.Sprintf("#%d %s", id, p.Command)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (a *App) callerName(ctx context.Context, c gateway.Caller) string {
	switch {
	case c.Token != 0:
		return fmt.Sprintf("token #%d", c.Token)
	case c.Process != 0:
		return a.processName(ctx, c.Process)
	}
	return c.Kind
}

// confirmTarget is where a provider tool call goes: its URL, command or path, else its input.
func confirmTarget(in map[string]any) string {
	for _, k := range []string{"url", "command", "file_path", "path", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	if len(in) == 0 {
		return ""
	}
	b, _ := json.Marshal(in)
	return untrusted.Cut(string(b), 300)
}

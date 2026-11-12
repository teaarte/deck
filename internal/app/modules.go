package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/tools"
)

// modules collects the commands not written in Go: those of module manifests with their HTTP and MCP tools, and
// deck's tools a running server describes; each manifest revision and grant is compiled once.
type modules struct {
	a        *App
	mu       sync.Mutex
	prepared map[preparedKey][]*registry.Command
}

// commands are the commands of the module name of a workspace, of every one when name is empty; deck's are the tools
// of its browser.
func (s *modules) commands(ctx context.Context, workspace int64, name string) ([]*registry.Command, error) {
	if name == registry.Builtin {
		return s.a.Browser.Commands(ctx), nil
	}
	var ps []db.Project
	if name == "" {
		all, err := s.a.Store.Q.ListWorkspaceProjects(ctx, workspace)
		if err != nil {
			return nil, err
		}
		ps = all
	} else {
		p, err := s.a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	}
	var out []*registry.Command
	if name == "" {
		out = s.a.Browser.Commands(ctx)
	}
	ms, failed := docs.All(ctx, s.a.Store.Q, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			slog.Error("project manifest", "project", p.Name, "err", failed[p.ID])
			continue
		}
		granted, err := docs.Grants(ctx, s.a.Store.Q, p.ID)
		if err != nil {
			slog.Error("project rights", "project", p.Name, "err", err)
			continue
		}
		out = append(out, s.compiled(p, m, m.Held(granted, manifest.Network, manifest.Programs))...)
		mcpCmds, err := s.a.MCP.Commands(ctx, s.a.Store.Q, p, m)
		if err != nil {
			return nil, err
		}
		for _, cmd := range mcpCmds {
			if err := registry.Prepare(cmd); err != nil {
				slog.Error("project tool", "tool", cmd.Name, "err", err)
				continue
			}
			out = append(out, cmd)
		}
	}
	return out, nil
}

// compiled are the commands and HTTP tools of a manifest as registry commands, compiled once per revision and the
// hosts its programs hold the network of, which its tools reach; a command that declares send needs that right.
func (s *modules) compiled(p db.Project, m *manifest.Manifest, hosts []string) []*registry.Command {
	key := preparedKey{p.ID, m, strings.Join(hosts, " ")}
	s.mu.Lock()
	cmds, ok := s.prepared[key]
	s.mu.Unlock()
	if ok {
		return cmds
	}
	for _, c := range m.Commands {
		need, sends := m.CommandNeed(c.Name)
		cmd := registry.Loose(p.Name+"."+c.Name, c.Title, c.Description, c.Callable(), c.InputSchema(),
			s.startHandler(p.ID, m, c.Name)).Retried()
		cmd.Result, cmd.ProjectID, cmd.Words = c.Result, p.ID, m.I18n
		if sends {
			cmd.Gate.Needs = func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
				return []registry.Need{need}, nil
			}
			cmd.Gate.Undo = tools.UndoOf(p.Name, c.Undo)
		} else {
			cmd.Stays()
		}
		if err := registry.Prepare(cmd); err != nil {
			slog.Error("project command", "command", cmd.Name, "err", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	for _, t := range m.Tools.HTTP {
		cmd := s.a.Tools.Command(p, t, hosts)
		if err := registry.Prepare(cmd); err != nil {
			slog.Error("project tool", "tool", cmd.Name, "err", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	s.mu.Lock()
	if s.prepared == nil || len(s.prepared) >= 256 {
		s.prepared = map[preparedKey][]*registry.Command{}
	}
	s.prepared[key] = cmds
	s.mu.Unlock()
	return cmds
}

type preparedKey struct {
	project  int64
	manifest *manifest.Manifest
	hosts    string
}

// startHandler runs a command of the manifest the registry checked the call against; the calling process
// or the finished process of a trigger becomes its parent.
func (s *modules) startHandler(projectID int64, m *manifest.Manifest, command string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		input, err := manifest.DecodeObject(raw)
		if err != nil {
			return nil, registry.Invalid("input: %v", err)
		}
		o := process.Origin{Parent: act.ProcessID, Trigger: act.TriggerID, Confirmed: act.Confirmed, Key: act.Key, Trial: act.Trial, Alone: act.Alone,
			Same: turnOf(ctx, act)}
		// Only the owner's own press stands for their yes.
		if act.Kind == registry.Owner && act.Allows != "" {
			var as []gateway.Allow
			if err := json.Unmarshal([]byte(act.Allows), &as); err != nil {
				return nil, registry.Invalid("allows: %v", err)
			}
			o.Allows = gateway.Allows(as)
		}
		st, err := s.a.Runner.Begin(ctx, projectID, m, command, input, o)
		if err != nil || st.Process == 0 || !turnOf(ctx, act) || m.Command(command).Executor.Type == process.KindAgent {
			return st, err
		}
		return s.a.Processes.Outcome(ctx, act, st)
	}
}

// turnOf says the caller is the turn of an agent calling now, which gets the outcome of a short command in its call.
func turnOf(ctx context.Context, act registry.Actor) bool {
	return gateway.Live(ctx) && act.ProcessID != 0 && (act.Kind == registry.Master || act.Kind == registry.Agent && act.Run == "")
}

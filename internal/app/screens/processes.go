package screens

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"

	"deck2/internal/process"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

func (s Sources) processItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	var ps []db.Process
	if sc.Record != 0 {
		p, err := s.Store.Q.GetProcess(ctx, sc.Record)
		if err == nil && names[p.ProjectID] != "" && (sc.Project == 0 || p.ProjectID == sc.Project) {
			ps = append(ps, p)
		}
	} else if len(sc.Filter) > 0 {
		got, err := s.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: sc.Workspace, ProjectID: filterProject(sc, names),
			State: sc.Filter["state"], Lim: 200})
		if err != nil {
			return nil, err
		}
		ps = got
	} else {
		open, err := s.Store.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project})
		if err != nil {
			return nil, err
		}
		recent, err := s.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, State: "", Lim: 200})
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
		it := map[string]any{"id": p.ID, "project": names[p.ProjectID], "command": p.Command, "kind": p.Kind, "state": p.State,
			"silent": p.SilentSinceMs.Valid, "started_at": p.StartedAt, "provider": p.Provider, "error": p.Error, "folder": p.Folder}
		acts := []map[string]any{{"verb": "open", "screen": "process", "record": p.ID}}
		if !finished(p.State) {
			acts = append(acts, call("stop", false, "deck.process_stop", map[string]any{"process": p.ID}))
		}
		if sc.Record != 0 {
			acts = acts[1:]
			if p.Kind == process.KindAgent && p.SessionID != "" && finished(p.State) {
				acts = append(acts, withInput(call("rework", true, "deck.process_rework", map[string]any{"process": p.ID}), "remark"))
			}
			if p.Kind == process.KindAgent && !finished(p.State) {
				acts = append(acts, withInput(call("tell", false, "deck.process_tell", map[string]any{"process": p.ID}), "text"))
			}
			it["terminal"] = s.Deck.Terminal(ctx, p)
		}
		it["_actions"] = acts
		out = append(out, it)
	}
	return out, nil
}

func finished(state string) bool {
	return slices.Contains([]string{process.StateDone, process.StateError, process.StateInterrupted, process.StateStopped}, state)
}

// logItems are the last lines of the log of a process.
func (s Sources) logItems(processID int64) ([]map[string]any, error) {
	out := []map[string]any{}
	f, err := os.Open(s.LogPath(processID))
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

// unattendedItems are what deck did without the owner, newest first; an entry with a command can be undone.
func (s Sources) unattendedItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	rows, err := s.Store.Q.ListUnattended(ctx, db.ListUnattendedParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, Lim: 50})
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
}

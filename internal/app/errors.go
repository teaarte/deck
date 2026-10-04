package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/app/access"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

// masterErrorPrefix keys the day a failed command of a project last went to the master; screenErrorPrefix, a
// block of a screen that failed on a revision of its manifest.
const (
	masterErrorPrefix = "master_error:"
	screenErrorPrefix = "screen_error:"
)

// errorsHook hands the failed processes of projects to the master.
type errorsHook struct {
	process.NoHook
	a *App
}

func (h errorsHook) Closed(ctx context.Context, p db.Process) { h.a.toMaster(ctx, p) }

// toMaster hands a failed process of a project to the master: an error, a result not by its schema, or a
// script failing the third time in a row; once a day per command, never for the master's own processes.
func (a *App) toMaster(ctx context.Context, p db.Process) {
	if p.Trial == 1 {
		return
	}
	failed := p.State == process.StateError || p.State == process.StateDone && p.NotBySchema == 1
	if !failed {
		return
	}
	if p.Kind == process.KindScript && p.State == process.StateError {
		states, err := a.Store.Q.RecentOutcomes(ctx, db.RecentOutcomesParams{ProjectID: p.ProjectID, Command: p.Command})
		if err != nil || len(states) < 3 || states[0] != process.StateError || states[1] != process.StateError || states[2] != process.StateError {
			return
		}
	}
	proj, err := a.Store.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return
	}
	key := fmt.Sprintf("%s%d.%s", masterErrorPrefix, proj.ID, p.Command)
	// The master's own failure waits for the owner instead.
	a.handOver(ctx, proj, key, 24*time.Hour, map[string]any{"process": p.ID}, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.New(ctx, q, p.ID, ask.KindWarning, master.Text("master_failed", struct{ Command, Error string }{p.Command, untrusted.Cut(p.Error, 500)}), "{}", time.Time{})
		return err
	})
}

// screenFailed puts a block of a module screen that failed into the health of the module and hands it to the
// master, once per block and revision of the manifest.
func (a *App) screenFailed(ctx context.Context, p db.Project, revision int64, name string, errs []string) {
	for _, e := range errs {
		block, _, _ := strings.Cut(e, ": ")
		key := fmt.Sprintf("%d.screen:%s:%s@%d", p.ID, name, block, revision)
		if !a.firstTime(ctx, p, screenErrorPrefix+key) {
			continue
		}
		health.Write(ctx, a.Store, health.Project(p), "screen:"+p.Name+"."+name, "error", e)
		a.handOver(ctx, p, masterErrorPrefix+key, 0, map[string]any{"project": p.Name, "screen": name, "error": e}, nil)
	}
}

// firstTime marks key of a project and says whether it was not marked yet.
func (a *App) firstTime(ctx context.Context, p db.Project, key string) bool {
	first := false
	_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: key}); err == nil {
			return nil
		}
		first = true
		return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: key, Value: store.Time(a.Cfg.Now())})
	})
	return first
}

// handOver gives an error of a project to the on_error command of the master, at most once in every while under
// key, forever when every is zero; an error of the master itself goes to own instead.
func (a *App) handOver(ctx context.Context, proj db.Project, key string, every time.Duration, input map[string]any,
	own func(context.Context, *db.Queries) error) {
	isOwn := proj.Builtin == 1
	if m, err := docs.Manifest(ctx, a.Store.Q, proj); err != nil || !isOwn && m.MasterErrors != nil && !*m.MasterErrors {
		return
	}
	var mp db.Project
	var mm *manifest.Manifest
	var err error
	if !isOwn {
		if mp, err = access.Master(ctx, a.Store.Q, proj.WorkspaceID); err != nil {
			return
		}
		if mm, err = docs.Manifest(ctx, a.Store.Q, mp); err != nil || mm.Command(mm.EventCommand(manifest.OnFailure)) == nil || mp.Paused == 1 {
			return
		}
	}
	now := a.Cfg.Now()
	due := true
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if last, err := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: proj.WorkspaceID, Name: key}); err == nil {
			if t, err := time.Parse(store.TimeLayout, last); err == nil && (every == 0 || now.Sub(t) < every) {
				due = false
				return nil
			}
		}
		if err := q.SetMark(ctx, db.SetMarkParams{WorkspaceID: proj.WorkspaceID, ProjectID: store.ID(proj.ID), Name: key, Value: store.Time(now)}); err != nil || !isOwn || own == nil {
			return err
		}
		return own(ctx, q)
	})
	if err != nil || !due || isOwn {
		return
	}
	if _, err := a.Runner.Start(context.WithoutCancel(ctx), mp.ID, mm, mm.EventCommand(manifest.OnFailure), input, process.Origin{}); err != nil {
		slog.Error("hand an error to the master", "project", proj.Name, "input", input, "err", err)
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.DeleteMark(ctx, db.DeleteMarkParams{WorkspaceID: proj.WorkspaceID, Name: key})
		})
	}
}

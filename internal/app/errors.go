package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"deck2/internal/app/docs"
	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

const errorCommand = "on_error"

// toMaster hands a failed process of a project to the master: an error, a result not by its schema, or a
// script failing the third time in a row; once a day per command, never for the master's own processes.
func (a *App) toMaster(ctx context.Context, processID int64) {
	p, err := a.Store.Q.GetProcess(ctx, processID)
	if err != nil || p.Trial == 1 {
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
	if m, err := docs.Manifest(ctx, a.Store.Q, proj); err != nil || m.MasterErrors != nil && !*m.MasterErrors {
		return
	}
	// The master's own failure waits for the owner instead.
	own := proj.Builtin == 1
	var mp db.Project
	var mm *manifest.Manifest
	if !own {
		if mp, err = a.masterProject(ctx, proj.WorkspaceID); err != nil {
			return
		}
		if mm, err = docs.Manifest(ctx, a.Store.Q, mp); err != nil || mm.Command(errorCommand) == nil || mp.Paused == 1 {
			return
		}
	}
	key := "master_error:" + proj.Name + "." + p.Command
	now := a.Cfg.Now()
	due := true
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if last, err := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: proj.WorkspaceID, Key: key}); err == nil {
			if t, err := time.Parse(store.TimeLayout, last); err == nil && now.Sub(t) < 24*time.Hour {
				due = false
				return nil
			}
		}
		if err := q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: proj.WorkspaceID, Key: key, Value: store.Time(now)}); err != nil || !own {
			return err
		}
		_, err := q.CreateAsk(ctx, db.CreateAskParams{ProcessID: p.ID, Kind: ask.KindWarning, Payload: "{}",
			Text: fmt.Sprintf("The master's command %s failed: %s", p.Command, untrusted.Cut(p.Error, 500))})
		return err
	})
	if err != nil || !due {
		return
	}
	if own {
		a.Bus.Publish("asks")
		return
	}
	if _, err := a.Runner.Start(context.WithoutCancel(ctx), mp.ID, mm, errorCommand, map[string]any{"process": p.ID}, process.Origin{}); err != nil {
		slog.Error("hand an error to the master", "process", p.ID, "project", master.Name, "err", err)
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeleteSetting(ctx, db.DeleteSettingParams{WorkspaceID: proj.WorkspaceID, Key: key})
			return err
		})
	}
}

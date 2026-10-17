package triggers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

// Fire starts the command of a trigger through the registry. Scheduled runs carry the trigger as their
// retry key, so a run does not start while the previous one is still going.
func (s *Service) Fire(ctx context.Context, t db.Trigger, input map[string]any) (int64, error) {
	if t.Kind == trigger.KindCron {
		input[manifest.RetryKey] = fmt.Sprintf("trigger:%d", t.ID)
	}
	return s.Start(ctx, t, input, 0)
}

// Start starts the command of a trigger as the trigger, with parent as the process whose finish fired it.
func (s *Service) Start(ctx context.Context, t db.Trigger, input map[string]any, parent int64) (int64, error) {
	p, err := s.St.Q.GetProject(ctx, t.ProjectID)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(input)
	act := registry.Actor{Kind: registry.Trigger, Workspace: p.WorkspaceID, ProjectID: p.ID, ProcessID: parent, TriggerID: t.ID}
	out, err := s.Gateway.CallAs(ctx, act, p.Name+"."+t.Command, raw)
	if err != nil {
		return 0, err
	}
	started, _ := out.(process.Started)
	return started.Process, nil
}

// After fires the triggers that follow a command which finished done with a result by its schema.
type After struct {
	process.NoHook
	S *Service
}

func (h After) Closed(ctx context.Context, p db.Process) {
	if p.State == process.StateDone && p.NotBySchema == 0 && p.Result != process.Empty && p.Alone == 0 {
		h.S.after(ctx, p)
	}
}

// after starts the commands that run when a command of the same project finishes, with its record when they take one.
func (s *Service) after(ctx context.Context, done db.Process) {
	p, err := s.St.Q.GetProject(ctx, done.ProjectID)
	if err != nil || p.Paused == 1 {
		return
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		slog.Error("after triggers", "project", p.Name, "err", err)
		return
	}
	rows, err := s.St.Q.ListProjectTriggers(ctx, p.ID)
	if err != nil {
		return
	}
	for _, t := range m.Triggers {
		if t.After != done.Command {
			continue
		}
		row, ok := trigger.Find(rows, t)
		if !ok || row.Paused == 1 {
			continue
		}
		input := manifest.TriggerInput(t, done.ID)
		if c := m.Command(t.Command); c != nil && manifest.FieldTypes(c.Input)["record"] == "integer" && input["record"] == nil {
			if record := s.recordOf(ctx, done); record != 0 {
				input["record"] = record
			}
		}
		if _, err := s.Start(ctx, row, input, done.ID); err != nil {
			slog.Error("after trigger", "project", p.Name, "command", t.Command, "after", done.ID, "err", err)
			text := fmt.Sprintf("deck did not start %s after process #%d of %s: %v", t.Command, done.ID, done.Command, err)
			_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(time.Now()), ProjectID: p.ID, Text: text})
			})
		}
	}
}

// recordOf is the record a finished process worked on: the record of its input, else the one record it created.
func (s *Service) recordOf(ctx context.Context, p db.Process) int64 {
	if record := process.RecordOf(p.Input); record != 0 {
		return record
	}
	ids, err := s.St.Q.CreatedRecords(ctx, db.CreatedRecordsParams{ProcessID: store.ID(p.ID), ProjectID: p.ProjectID})
	if err != nil || len(ids) != 1 {
		return 0
	}
	return ids[0]
}

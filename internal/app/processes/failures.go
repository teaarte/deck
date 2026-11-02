package processes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// masterErrorPrefix keys the day a failed command of a project last went to the master; screenErrorPrefix, a
// block of a screen that failed on a revision of its manifest; incidentPrefix, an incident of a process.
const (
	masterErrorPrefix = "master_error:"
	screenErrorPrefix = "screen_error:"
	incidentPrefix    = "incident:"
)

// Hook hands the failed processes of projects to the master.
func (s *Service) Hook() process.Hook { return failures{s: s} }

type failures struct {
	process.NoHook
	s *Service
}

func (h failures) Closed(ctx context.Context, p db.Process) {
	h.s.toMaster(ctx, p)
	if p.SubmittedAt.Valid && p.State != process.StateDone {
		h.s.Incident(ctx, p, process.IncidentNotDone, fmt.Sprintf("process #%d of %s handed in its result, then closed as %s: %s",
			p.ID, p.Command, p.State, untrusted.Cut(p.Error, 300)))
	}
}

// Incident hands what deck noticed went wrong around a process of a project to the master, once a process and kind;
// deck mends none itself.
func (s *Service) Incident(ctx context.Context, p db.Process, kind, text string) {
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil || p.Trial == 1 || proj.Builtin == 1 {
		return
	}
	s.handOver(ctx, proj, manifest.OnIncident, fmt.Sprintf("%s%d.%s", incidentPrefix, p.ID, kind), 0,
		map[string]any{"process": p.ID, "incident": kind, "text": text}, nil)
}

// Due hands the master the processes that handed in their result and still run long after.
func (s *Service) Due(ctx context.Context, now time.Time) {
	ps, err := s.St.Q.ListSubmittedRunning(ctx, sql.NullString{String: store.Time(now.Add(-process.SubmittedLinger)), Valid: true})
	if err != nil {
		slog.Error("processes running with their result in", "err", err)
		return
	}
	for _, p := range ps {
		s.Incident(ctx, p, process.IncidentRunning, fmt.Sprintf("process #%d of %s handed in its result at %s and still runs",
			p.ID, p.Command, p.SubmittedAt.String))
	}
}

// toMaster hands a failed process of a project to the master with the type of its failure, once a day per command
// and type: a script only on its third failure in a row, never a process of the master.
func (s *Service) toMaster(ctx context.Context, p db.Process) {
	if p.Trial == 1 {
		return
	}
	failed := p.State == process.StateError || p.State == process.StateDone && p.NotBySchema == 1
	// A failure of deck or of a model is the owner's in health, and a model's goes on another model by itself.
	if whose := fault.Whose(p.ErrorType); !failed || whose == fault.OfDeck || whose == fault.OfModel {
		return
	}
	if p.Kind == process.KindScript && p.State == process.StateError {
		states, err := s.St.Q.RecentOutcomes(ctx, db.RecentOutcomesParams{ProjectID: p.ProjectID, Command: p.Command})
		if err != nil || len(states) < 3 || states[0] != process.StateError || states[1] != process.StateError || states[2] != process.StateError {
			return
		}
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return
	}
	key := fmt.Sprintf("%s%d.%s.%s", masterErrorPrefix, proj.ID, p.Command, p.ErrorType)
	input := map[string]any{"process": p.ID, "type": p.ErrorType, "whose": fault.Whose(p.ErrorType)}
	// The master's own failure goes to the owner's feed instead.
	s.handOver(ctx, proj, manifest.OnFailure, key, 24*time.Hour, input, func(ctx context.Context, q *db.Queries) error {
		return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(s.Now()), ProjectID: p.ProjectID,
			Text: fmt.Sprintf("The master's command %s failed: %s", p.Command, untrusted.Cut(p.Error, 500))})
	})
}

// Screen puts a block of a module screen that failed into the health of the module and hands it to the
// master, once per block and revision of the manifest; the marks of the other revisions go.
func (s *Service) Screen(ctx context.Context, p db.Project, revision int64, name string, errs []string) {
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, prefix := range []string{screenErrorPrefix, masterErrorPrefix} {
			err := q.DeleteMarksOfOtherRevisions(ctx, db.DeleteMarksOfOtherRevisionsParams{WorkspaceID: p.WorkspaceID,
				Prefix: fmt.Sprintf("%s%d.screen:", prefix, p.ID), Revision: fmt.Sprintf("@%d", revision)})
			if err != nil {
				return err
			}
		}
		return nil
	})
	for _, e := range errs {
		block, _, _ := strings.Cut(e, ": ")
		key := fmt.Sprintf("%d.screen:%s:%s@%d", p.ID, name, block, revision)
		subject := health.Screen(p.Name, name)
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if _, err := health.Get(ctx, q, health.Project(p), subject); !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			_, err := health.Set(ctx, q, health.Project(p), subject, health.Error, e)
			return err
		})
		if err != nil {
			slog.Error("write health", "err", err)
		}
		if !s.firstTime(ctx, p, screenErrorPrefix+key) {
			continue
		}
		s.handOver(ctx, p, manifest.OnFailure, masterErrorPrefix+key, 0, map[string]any{"project": p.Name, "screen": name, "error": e}, nil)
	}
}

// ScreenDrawn clears the health of a screen of a project that drew without errors: all of it, or with good the
// blocks it read, among them the one its health names.
func (s *Service) ScreenDrawn(ctx context.Context, p db.Project, name string, good []string) {
	subject := health.Screen(p.Name, name)
	h, err := health.Get(ctx, s.St.Q, health.Project(p), subject)
	if err != nil {
		return
	}
	if block, _, _ := strings.Cut(h.Error, ": "); good != nil && !slices.Contains(good, block) {
		return
	}
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return health.Clear(ctx, q, health.Project(p), subject)
	})
}

// firstTime marks key of a project and says whether it was not marked yet.
func (s *Service) firstTime(ctx context.Context, p db.Project, key string) bool {
	first := false
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: key}); err == nil {
			return nil
		}
		first = true
		return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: key, Value: store.Time(s.Now())})
	})
	return first
}

// handOver gives an error of a project to the on_error command of the master, at most once in every while under
// key, forever when every is zero; an error of the master itself goes to own instead.
func (s *Service) handOver(ctx context.Context, proj db.Project, on, key string, every time.Duration, input map[string]any,
	own func(context.Context, *db.Queries) error) {
	isOwn := proj.Builtin == 1
	if _, err := docs.Manifest(ctx, s.St.Q, proj); err != nil {
		return
	}
	var mp db.Project
	var mm *manifest.Manifest
	var err error
	if !isOwn {
		if mp, err = access.Master(ctx, s.St.Q, proj.WorkspaceID); err != nil {
			return
		}
		if mm, err = docs.Manifest(ctx, s.St.Q, mp); err != nil || mm.Command(mm.EventCommand(on)) == nil || mp.Paused == 1 {
			return
		}
	}
	now := s.Now()
	if last, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: proj.WorkspaceID, Name: key}); err == nil {
		if t, err := time.Parse(store.TimeLayout, last); err == nil && (every == 0 || now.Sub(t) < every) {
			return
		}
	}
	mark := func(ctx context.Context, q *db.Queries) error {
		return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: proj.WorkspaceID, ProjectID: store.ID(proj.ID), Name: key, Value: store.Time(now)})
	}
	if isOwn {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if err := mark(ctx, q); err != nil || own == nil {
				return err
			}
			return own(ctx, q)
		})
		return
	}
	// The mark follows the start, so a crash between them may hand the error over twice but never loses it; the key
	// keeps two failures at once to one run.
	if _, err := s.Runner.Start(context.WithoutCancel(ctx), mp.ID, mm, mm.EventCommand(on), input, process.Origin{Key: key}); err != nil {
		slog.Error("hand an error to the master", "project", proj.Name, "input", input, "err", err)
		return
	}
	_ = s.St.Tx(context.WithoutCancel(ctx), mark)
}

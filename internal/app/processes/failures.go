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
	masterErrorPrefix = "processes:master_error:"
	screenErrorPrefix = "processes:screen_error:"
	incidentPrefix    = "processes:incident:"
)

// Hook hands the failed processes of projects to the master.
func (s *Service) Hook() process.Hook { return failures{s: s} }

type failures struct {
	process.NoHook
	s *Service
}

func (h failures) Closed(ctx context.Context, p db.Process) error {
	err := h.s.toMaster(ctx, p)
	if p.SubmittedAt.Valid && p.State != process.StateDone {
		err = errors.Join(err, h.s.Incident(ctx, p, process.IncidentNotDone, fmt.Sprintf("process #%d of %s handed in its result, then closed as %s: %s",
			p.ID, p.Command, p.State, untrusted.Cut(p.Error, 300))))
	}
	return err
}

// Incident hands what deck noticed went wrong around a process of a project to the master, once a process and kind;
// deck mends none itself.
func (s *Service) Incident(ctx context.Context, p db.Process, kind, text string) error {
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (p.Trial == 1 || proj.Builtin == 1) {
		return nil
	}
	if err != nil {
		return err
	}
	note := text
	if kind == process.IncidentAfter {
		// deck noted a trigger it did not start already.
		note = ""
	}
	return s.handOver(ctx, proj, manifest.OnIncident, fmt.Sprintf("%s%d.%s", incidentPrefix, p.ID, kind), 0,
		map[string]any{"process": p.ID, "incident": kind, "text": text}, note, nil)
}

// Due hands the master the processes that handed in their result and still run long after.
func (s *Service) Due(ctx context.Context, now time.Time) {
	ps, err := s.St.Q.ListSubmittedRunning(ctx, store.At(now.Add(-process.SubmittedLinger)))
	if err != nil {
		slog.Error("processes running with their result in", "err", err)
		return
	}
	for _, p := range ps {
		if err := s.Incident(ctx, p, process.IncidentRunning, fmt.Sprintf("process #%d of %s handed in its result at %s and still runs",
			p.ID, p.Command, p.SubmittedAt.String)); err != nil {
			slog.Error("hand the master a process running with its result in", "process", p.ID, "err", err)
		}
	}
}

// toMaster hands a failed process of a project to the master with the type of its failure, once a day per command
// and type: a script only on its third failure in a row, never a process of the master.
func (s *Service) toMaster(ctx context.Context, p db.Process) error {
	if p.Trial == 1 {
		return nil
	}
	failed := p.State == process.StateError || p.State == process.StateDone && p.NotBySchema == 1
	// A failure of deck or of a model is the owner's in health, and a model's goes on another model by itself.
	if whose := fault.Whose(p.ErrorType); !failed || whose == fault.OfDeck || whose == fault.OfModel {
		return nil
	}
	if p.Kind == process.KindScript && p.State == process.StateError {
		states, err := s.St.Q.RecentOutcomes(ctx, db.RecentOutcomesParams{ProjectID: p.ProjectID, Command: p.Command})
		if err != nil {
			return err
		}
		if len(states) < 3 || states[0] != process.StateError || states[1] != process.StateError || states[2] != process.StateError {
			return nil
		}
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s%d.%s.%s", masterErrorPrefix, proj.ID, p.Command, p.ErrorType)
	input := map[string]any{"process": p.ID, "type": p.ErrorType, "whose": fault.Whose(p.ErrorType)}
	// The master's own failure goes to the owner's feed instead.
	// A failed process the master does not take stays among the failures of the cockpit, which the owner sees.
	return s.handOver(ctx, proj, manifest.OnFailure, key, 24*time.Hour, input, "", func(ctx context.Context, q *db.Queries) error {
		return store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(s.Now()), ProjectID: p.ProjectID,
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
			_, err := health.Set(ctx, q, s.Now(), health.Project(p), subject, health.Error, e)
			return err
		})
		if err != nil {
			slog.Error("write health", "err", err)
		}
		// The mark follows the hand-over, as in handOver: a fall between them hands it over again, never loses it.
		mark := db.SetMarkParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: screenErrorPrefix + key, Value: store.Time(s.Now())}
		_, err = s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: mark.Name})
		if err == nil {
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = s.handOver(ctx, p, manifest.OnFailure, masterErrorPrefix+key, 0, map[string]any{"project": p.Name, "screen": name, "error": e}, "", nil)
		}
		if err == nil {
			err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.SetMark(ctx, mark) })
		}
		if err != nil {
			slog.Error("hand the master a block that failed", "project", p.Name, "screen", name, "err", err)
		}
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

// handOver gives an error of a project to the master's command for on, once in every while under key (forever with
// every zero); the master's own goes to own, one the master does not take (nor starts) to the owner's feed as note.
func (s *Service) handOver(ctx context.Context, proj db.Project, on, key string, every time.Duration, input map[string]any,
	note string, own func(context.Context, *db.Queries) error) error {
	isOwn := proj.Builtin == 1
	if _, err := docs.Manifest(ctx, s.St.Q, proj); err != nil {
		return nil
	}
	now := s.Now()
	last, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: proj.WorkspaceID, Name: key})
	if err == nil {
		if t, err := store.ParseTime(last); err == nil && (every == 0 || now.Sub(t) < every) {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	mark := func(ctx context.Context, q *db.Queries) error {
		return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: proj.WorkspaceID, ProjectID: store.ID(proj.ID), Name: key, Value: store.Time(now)})
	}
	if isOwn {
		return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if err := mark(ctx, q); err != nil || own == nil {
				return err
			}
			return own(ctx, q)
		})
	}
	untaken := func(why string) error {
		if note == "" {
			return nil
		}
		return s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			if err := mark(ctx, q); err != nil {
				return err
			}
			return store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(now), ProjectID: proj.ID,
				Text: fmt.Sprintf("%s; the master did not take it: %s", note, why)})
		})
	}
	mp, err := access.Master(ctx, s.St.Q, proj.WorkspaceID)
	if err != nil {
		return untaken("there is no master")
	}
	mm, err := docs.Manifest(ctx, s.St.Q, mp)
	switch {
	case err != nil:
		return untaken(fmt.Sprintf("its manifest does not read: %v", err))
	case mp.Paused == 1:
		return untaken("it is paused")
	case mm.Command(mm.EventCommand(on)) == nil:
		return untaken(fmt.Sprintf("it has no command for %s", on))
	}
	// The mark follows the start, so a crash between them may hand the error over twice but never loses it; the key
	// keeps two failures at once to one run.
	if _, err := s.Runner.Start(context.WithoutCancel(ctx), mp.ID, mm, mm.EventCommand(on), input, process.Origin{Key: key}); err != nil {
		slog.Error("hand an error to the master", "project", proj.Name, "input", input, "err", err)
		return untaken(fmt.Sprintf("it did not start: %v", err))
	}
	return s.St.Tx(context.WithoutCancel(ctx), mark)
}

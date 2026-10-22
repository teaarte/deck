package documents

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// docChanged lists a change made without the owner of a script, or of a document an agent writes by the writes of its
// manifest, undone by returning to the revision before n.
func (dc *Service) docChanged(ctx context.Context, q *db.Queries, act registry.Actor, d db.Document, n int64, reason string) error {
	byWrites := act.Kind == registry.Agent && d.Kind != docs.KindArtifact && d.Kind != docs.KindMemory
	if d.Kind != docs.KindScript && !byWrites || !d.ProjectID.Valid || d.RecordID.Valid {
		return nil
	}
	prev := db.Document{ID: d.ID, Current: n - 1}
	what := "document"
	if d.Kind == docs.KindScript {
		what = "script"
	}
	return dc.Changed(ctx, q, act, d.ProjectID.Int64, prev, n, fmt.Sprintf("%s changed %s %s", act.Author(), what, d.Name), reason)
}

// PrepareLater records the health of the script commands of a project in the background.
func (dc *Service) PrepareLater(projectID int64) {
	dc.Go(func() {
		if err := dc.Programs(dc.Ctx, projectID); err != nil && dc.Ctx.Err() == nil {
			slog.Error("prepare scripts", "project", projectID, "err", err)
		}
	})
}

type programsCheckIn struct {
	Project string `type:"project" desc:"Project name"`
}

func (dc *Service) programsCheck(ctx context.Context, act registry.Actor, in programsCheckIn) (any, error) {
	p, err := access.Project(ctx, dc.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if err := dc.Programs(ctx, p.ID); err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "checked": true}, nil
}

// Programs looks for the programs of the scripts of a project and writes the health of its script commands.
func (dc *Service) Programs(ctx context.Context, projectID int64) error {
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, dc.St.Q, p)
	if err != nil {
		return err
	}
	problems := script.Check(m)
	sbErr := dc.Sandbox.Check()
	known, err := dc.St.Q.ListHealth(ctx, store.ID(p.WorkspaceID))
	if err != nil {
		return err
	}
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		status, text := "ok", ""
		if sbErr != nil {
			status, text = "error", dc.Sandbox.Name()+": "+sbErr.Error()
		}
		if _, err := health.Set(ctx, q, health.Of{}, health.Sandbox, status, text); err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, c := range m.Commands {
			if c.Executor.Type != "script" {
				continue
			}
			subject := health.Command(p.Name, c.Name)
			keep[subject] = true
			status, text := scriptHealth(&c, problems, sbErr)
			if _, err := health.Set(ctx, q, health.Project(p), subject, status, text); err != nil {
				return err
			}
		}
		for _, row := range known {
			name, own := strings.CutPrefix(row.Health.Subject, health.Command(p.Name, ""))
			if h := row.Health; h.ProjectID.Int64 == p.ID && own && !keep[h.Subject] && m.Command(name) == nil {
				if err := health.Clear(ctx, q, health.Project(p), h.Subject); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}

func scriptHealth(c *manifest.Command, problems map[string]error, sbErr error) (string, string) {
	if sbErr != nil {
		return "error", "the script sandbox does not work: " + sbErr.Error()
	}
	if err := problems[c.Executor.Script]; err != nil {
		return "error", err.Error()
	}
	return "ok", ""
}

package app

import (
	"context"
	"log/slog"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/script"
	"deck2/internal/store/db"
)

const sandboxHealth = "sandbox"

func commandHealth(project, command string) string { return "command:" + project + "." + command }

// prepareLater records the health of the script commands of a project in the background.
func (a *App) prepareLater(projectID int64) {
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		if err := a.prepareScripts(a.ctx, projectID); err != nil && a.ctx.Err() == nil {
			slog.Error("prepare scripts", "project", projectID, "err", err)
		}
	}()
}

func (a *App) prepareScripts(ctx context.Context, projectID int64) error {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return err
	}
	problems := script.Check(m)
	sbErr := a.Scripts.Sandbox.Check()
	known, err := a.Store.Q.ListHealth(ctx)
	if err != nil {
		return err
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		status, text := "ok", ""
		if sbErr != nil {
			status, text = "error", a.Scripts.Sandbox.Name()+": "+sbErr.Error()
		}
		if err := health.Set(ctx, q, sandboxHealth, status, text); err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, c := range m.Commands {
			if c.Executor.Type != "script" {
				continue
			}
			subject := commandHealth(p.Name, c.Name)
			keep[subject] = true
			status, text := scriptHealth(&c, problems, sbErr)
			if err := health.Set(ctx, q, subject, status, text); err != nil {
				return err
			}
		}
		for _, h := range known {
			if strings.HasPrefix(h.Subject, commandHealth(p.Name, "")) && !keep[h.Subject] {
				if err := q.DeleteHealth(ctx, h.Subject); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		a.Bus.Publish("health")
	}
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

package app

import (
	"context"
	"log/slog"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

const sandboxHealth = "sandbox"

func commandHealth(project, command string) string { return "command:" + project + "." + command }

// prepareLater installs the script packages of a project in the background and records the health of its
// script commands; a script run waits for an install that is still going.
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
	m, err := loadManifest(ctx, a.Store.Q, p)
	if err != nil {
		return err
	}
	problems := a.Scripts.Ensure(ctx, p.ID, m)
	if ctx.Err() != nil {
		return ctx.Err()
	}
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
		if err := q.SetHealth(ctx, db.SetHealthParams{Subject: sandboxHealth, Status: status, Error: text}); err != nil {
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
			if err := q.SetHealth(ctx, db.SetHealthParams{Subject: subject, Status: status, Error: text}); err != nil {
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

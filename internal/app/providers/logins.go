package providers

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

// loginSet adds another subscription login of a CLI: a connection of api cli with its own sign-in and homes, and its
// own model of the same name, the CLI's default, which work is assigned to and the owner signs in by.
func (s *Service) loginSet(ctx context.Context, act registry.Actor, in connectionSetIn) (any, error) {
	in.API = models.APICLI
	_, ok := s.builtinNamed(in.Agent).(provider.Logins)
	switch {
	case in.Agent == "":
		return nil, registry.Invalid("a login of a CLI names its agent: claude-code or codex")
	case !ok:
		return nil, registry.Invalid("deck signs in to %s only once on this machine", in.Agent)
	case !schemas.ProjectName.MatchString(in.Name) || s.builtinNamed(in.Name) != nil:
		return nil, registry.Invalid("a login is named with lowercase letters, digits and '-', not %q or a built-in provider", in.Name)
	case in.URL != "" || in.Key != "" || in.Node != "" || in.Local:
		return nil, registry.Invalid("a login of a CLI takes no url, key, node or local: the owner signs in to it with agent_login")
	}
	old, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name})
	switch {
	case err == nil && old.Api == models.APICLI && old.Agent == in.Agent:
		return map[string]any{"connection": in.Name, "agent": in.Agent}, nil
	case err == nil:
		return nil, registry.Invalid("connection %s is already there; name the login another way", in.Name)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	if _, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
		return nil, registry.Invalid("model %s is already there; a login is a model of its own name too, so name it another way", in.Name)
	}
	if n, err := s.St.Q.ModelInOtherWorkspace(ctx, db.ModelInOtherWorkspaceParams{Name: in.Name, WorkspaceID: act.Workspace}); err != nil {
		return nil, err
	} else if n > 0 {
		return nil, registry.Invalid("another workspace has a provider %q; its health and self-check go by name, so pick another", in.Name)
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: models.APICLI, Agent: in.Agent})
		if err != nil {
			return err
		}
		if err := health.Clear(ctx, q, health.Of{}, health.Provider(in.Name)); err != nil {
			return err
		}
		_, err = q.AddModel(ctx, db.AddModelParams{WorkspaceID: act.Workspace, ConnectionID: c.ID, Name: in.Name, Kind: models.KindText,
			Options: "{}", Unit: models.UnitToken, Agent: in.Agent, Subscription: 1})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"connection": in.Name, "agent": in.Agent}, nil
}

// loginDelete removes another login of a CLI with its own model, its sign-in and homes; the models the owner added
// on it go first, and no process may run on it.
func (s *Service) loginDelete(ctx context.Context, act registry.Actor, c db.Connection) (any, error) {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		rows, err := q.ListModels(ctx, act.Workspace)
		if err != nil {
			return err
		}
		for _, m := range rows {
			if m.Connection.ID == c.ID && m.Model.Name != c.Name {
				return registry.Invalid("model %s is on login %s; remove it first", m.Model.Name, c.Name)
			}
		}
		active, err := q.CountActiveOnProvider(ctx, db.CountActiveOnProviderParams{Provider: c.Name, WorkspaceID: act.Workspace})
		if err != nil {
			return err
		}
		if active > 0 {
			return registry.Invalid("%d processes still run on %s; stop them or let them finish first", active, c.Name)
		}
		if _, err := q.DeleteModel(ctx, db.DeleteModelParams{WorkspaceID: act.Workspace, Name: c.Name}); err != nil {
			return err
		}
		if err := s.Unassign(ctx, q, act.Workspace, c.Name); err != nil {
			return err
		}
		if err := q.DeleteModelHealth(ctx, db.DeleteModelHealthParams{WorkspaceID: store.ID(act.Workspace), Model: health.Model(c.Name), Provider: health.Provider(c.Name), Name: c.Name}); err != nil {
			return err
		}
		if err := health.Clear(ctx, q, health.Of{}, health.Limit(c.Name)); err != nil {
			return err
		}
		_, err = q.DeleteConnection(ctx, db.DeleteConnectionParams{WorkspaceID: act.Workspace, Name: c.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if cli, ok := s.builtinNamed(c.Agent).(provider.Logins); ok {
		if err := cli.Forget(c.Name); err != nil {
			return nil, err
		}
	}
	return map[string]any{"removed": c.Name}, nil
}

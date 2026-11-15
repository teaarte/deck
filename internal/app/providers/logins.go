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
	if old, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && added(old) && old.Agent == in.Agent {
		return map[string]any{"connection": in.Name, "agent": in.Agent}, nil
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name})
		switch {
		case err == nil:
			return registry.Invalid("connection %s is already there; name the login another way", in.Name)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
			return registry.Invalid("model %s is already there; a login is a model of its own name too, so name it another way", in.Name)
		}
		spaces, err := q.ListWorkspaces(ctx)
		if err != nil {
			return err
		}
		for _, ws := range spaces {
			if _, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: ws, Name: in.Name}); err == nil && ws != act.Workspace {
				return registry.Invalid("another workspace has a connection %q; a login's sign-in and health go by name, so pick another", in.Name)
			}
		}
		if n, err := q.ModelInOtherWorkspace(ctx, db.ModelInOtherWorkspaceParams{Name: in.Name, WorkspaceID: act.Workspace}); err != nil {
			return err
		} else if n > 0 {
			return registry.Invalid("another workspace has a provider %q; its health and self-check go by name, so pick another", in.Name)
		}
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
	// A folder no login held, as of one removed while its sign-in ran, is not this new login's.
	if err := s.builtinNamed(in.Agent).(provider.Logins).Forget(in.Name); err != nil {
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
		// A module's answer to this login does not hold for another one of the same name later.
		if err := q.DeleteLoginGrants(ctx, db.DeleteLoginGrantsParams{Subject: c.Name, WorkspaceID: act.Workspace}); err != nil {
			return err
		}
		_, err = q.DeleteConnection(ctx, db.DeleteConnectionParams{WorkspaceID: act.Workspace, Name: c.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.check, c.Name)
	s.mu.Unlock()
	if cli, ok := s.builtinNamed(c.Agent).(provider.Logins); ok {
		if err := cli.Forget(c.Name); err != nil {
			return nil, err
		}
	}
	return map[string]any{"removed": c.Name}, nil
}

// added says a connection is a login of a CLI the owner added, not the built-in one of its name.
func added(c db.Connection) bool { return c.Api == models.APICLI && c.Agent != "" && c.Name != c.Agent }

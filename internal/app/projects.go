package app

import (
	"context"
	"deck2/internal/app/access"
	"fmt"
	"log/slog"

	"deck2/internal/app/board"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
)

// Text is the current text of a project document by kind and name, of any kind for ""; empty when missing.
func (a *App) Text(ctx context.Context, projectID int64, kind, name string) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), kind, name)
}

// Prompt is the current md prompt of a project; empty when it has none.
func (a *App) Prompt(ctx context.Context, projectID int64) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName)
}

type parsed struct {
	m    *manifest.Manifest
	text []byte
}

// parseManifest validates a manifest and returns the text to store, upgraded to the current format; only the
// built-in project answers events of deck.
func parseManifest(raw []byte, builtin bool) (parsed, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return parsed{}, registry.Invalid("deck: %v", err)
	}
	for _, t := range m.Triggers {
		if t.On != "" && !builtin {
			return parsed{}, registry.Invalid("deck: a trigger on %s belongs to the built-in project %s", t.On, master.Name)
		}
	}
	return parsed{m: m, text: m.Text()}, nil
}

// CreateProject validates the manifest, stores it and the prompt as first revisions and registers triggers.
// The owner's project is enabled; anyone else's is a draft.
func (a *App) CreateProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt string) (db.Project, error) {
	status := store.ProjectDraft
	if act.Kind == registry.Owner {
		status = store.ProjectEnabled
	}
	return a.createProject(ctx, act, name, raw, prompt, status, nil)
}

// createProject stores a new project; more stores further documents in the same transaction before the checks.
func (a *App) createProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt, status string,
	more func(ctx context.Context, q *db.Queries, p db.Project) error) (db.Project, error) {
	if !access.ProjectName.MatchString(name) || name == registry.Builtin {
		return db.Project{}, registry.Invalid("deck: project name %q must be lowercase letters, digits and '-', start with a letter, and not be %q", name, registry.Builtin)
	}
	pm, err := parseManifest(raw, false)
	if err != nil {
		return db.Project{}, err
	}
	var p db.Project
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name}); err == nil {
			return registry.Invalid("deck: project %q already exists", name)
		}
		if err := checkReads(ctx, q, act.Workspace, name, pm.m); err != nil {
			return err
		}
		if err := a.checkToolFit(ctx, q, act.Workspace, 0, pm.m); err != nil {
			return err
		}
		if p, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: act.Workspace, Name: name, Status: status}); err != nil {
			return err
		}
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
		if _, _, err := docs.Save(ctx, q, ref, docs.KindManifest, string(pm.text), act.Author(), "create project"); err != nil {
			return err
		}
		if prompt != "" {
			ref.Name = docs.PromptName
			if _, _, err := docs.Save(ctx, q, ref, docs.KindPrompt, prompt, act.Author(), "create project"); err != nil {
				return err
			}
		}
		if more != nil {
			if err := more(ctx, q, p); err != nil {
				return err
			}
		}
		return a.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil {
		return db.Project{}, err
	}
	a.Bus.Publish("projects")
	a.prepareLater(p.ID)
	return p, nil
}

// Call is a registry call frozen for the owner to allow later.
type Call struct {
	Command string         `json:"command"`
	Input   map[string]any `json:"input"`
}

// Saved is a stored manifest revision, or the ask it waits on when the change widens the project's rights.
type Saved struct {
	Revision int64    `json:"revision,omitempty"`
	Waiting  int64    `json:"waiting,omitempty"`
	Widens   []string `json:"widens,omitempty"`
}

// SaveManifest checks a manifest and saves it with its triggers and migrated records in one transaction; a
// change that widens rights waits for the owner as call; nonzero ifRevision must still be current.
func (a *App) SaveManifest(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64, call Call) (Saved, error) {
	pm, err := parseManifest(raw, p.Builtin == 1)
	if err != nil {
		return Saved{}, err
	}
	plan, err := a.migrate(ctx, p, pm.m, revertTo)
	if err != nil {
		return Saved{}, err
	}
	var saved Saved
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := checkReads(ctx, q, p.WorkspaceID, p.Name, pm.m); err != nil {
			return err
		}
		if err := a.checkToolFit(ctx, q, p.WorkspaceID, p.ID, pm.m); err != nil {
			return err
		}
		if err := checkRecords(ctx, q, p, pm.m, plan); err != nil {
			return err
		}
		if err := board.CheckLanes(ctx, q, p, pm.m); err != nil {
			return err
		}
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
		cur, err := q.FindDocument(ctx, ref.Find())
		if err != nil {
			return err
		}
		if ifRevision != 0 && cur.Current != ifRevision {
			return registry.Conflict("deck: the manifest of %s is at revision %d now, not %d", p.Name, cur.Current, ifRevision)
		}
		w, err := a.widens(ctx, q, act, p.ID, pm.m)
		if err != nil || len(w) > 0 {
			if err == nil {
				saved, err = a.ask(ctx, q, act, p, cur.Current, w, reason, call)
			}
			return err
		}
		var n int64
		if _, n, err = docs.Save(ctx, q, ref, docs.KindManifest, string(pm.text), act.Author(), reason); err != nil {
			return err
		}
		saved.Revision = n
		for name, mg := range plan {
			if err := applyMigration(ctx, q, store.Time(a.Cfg.Now()), p.ID, pm.m.Collection(name), mg, cur.Current, n); err != nil {
				return err
			}
		}
		if err := a.unattended(ctx, q, act, p.ID, cur, n, fmt.Sprintf("%s changed the manifest of %s", act.Author(), p.Name), reason); err != nil {
			return err
		}
		return a.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil || saved.Waiting != 0 {
		if saved.Waiting != 0 {
			a.Bus.Publish("asks")
		}
		return saved, err
	}
	a.Bus.Publish("projects")
	if len(plan) > 0 {
		a.Bus.Publish("collection")
	}
	a.prepareLater(p.ID)
	return saved, nil
}

// Reconcile brings stored triggers in line with the current manifests; run it on start.
func (a *App) Reconcile(ctx context.Context) error {
	ps, err := a.Store.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			m, err := docs.Manifest(ctx, q, p)
			if err != nil {
				return err
			}
			return a.Scheduler.Sync(ctx, q, p.ID, m)
		})
		if err != nil {
			slog.Error("reconcile triggers", "project", p.Name, "err", err)
		}
		a.prepareLater(p.ID)
	}
	return nil
}

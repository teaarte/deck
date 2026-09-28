package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// loadManifest parses the current manifest of a project.
func loadManifest(ctx context.Context, q *db.Queries, p db.Project) (*manifest.Manifest, error) {
	s := projectScope(p)
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Name: ManifestName, Kind: KindManifest})
	if errors.Is(err, sql.ErrNoRows) {
		return manifest.Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	m, err := manifest.Parse([]byte(row.Text))
	if err == nil {
		m.Revision = row.N
	}
	return m, err
}

// ManifestAt is a stored revision of the manifest of a project; revision 0 is the current one.
func (a *App) ManifestAt(ctx context.Context, projectID, revision int64) (*manifest.Manifest, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil || revision == 0 {
		if err != nil {
			return nil, err
		}
		return loadManifest(ctx, a.Store.Q, p)
	}
	d, err := a.Store.Q.FindDocument(ctx, DocRef{Scope: projectScope(p), Name: ManifestName}.find())
	if err != nil {
		return nil, err
	}
	r, err := getRevision(ctx, a.Store.Q, d, revision)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Parse([]byte(r.Text))
	if err == nil {
		m.Revision = revision
	}
	return m, err
}

// Manifest is the current manifest of a project, read outside any transaction.
func (a *App) Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return loadManifest(ctx, a.Store.Q, p)
}

// Text is the current text of a project document by kind and name, of any kind for ""; empty when missing.
func (a *App) Text(ctx context.Context, projectID int64, kind, name string) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return currentText(ctx, a.Store.Q, projectScope(p), kind, name)
}

// Prompt is the current md prompt of a project; empty when it has none.
func (a *App) Prompt(ctx context.Context, projectID int64) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return currentText(ctx, a.Store.Q, projectScope(p), KindPrompt, PromptName)
}

type parsed struct {
	m      *manifest.Manifest
	text   []byte
	legacy string
}

// parseManifest validates a manifest and returns the text to store, upgraded to the current format.
func parseManifest(raw []byte) (parsed, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return parsed{}, registry.Invalid("deck: %v", err)
	}
	return parsed{m: m, text: m.Text(), legacy: manifest.LegacyDescription(raw)}, nil
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
	if !projectName.MatchString(name) || name == registry.Builtin {
		return db.Project{}, registry.Invalid("deck: project name %q must be lowercase letters, digits and '-', start with a letter, and not be %q", name, registry.Builtin)
	}
	pm, err := parseManifest(raw)
	if err != nil {
		return db.Project{}, err
	}
	if prompt == "" {
		prompt = pm.legacy
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
		ref := DocRef{Scope: projectScope(p), Name: ManifestName}
		if _, _, err := SaveDocument(ctx, q, ref, KindManifest, string(pm.text), act.Author(), "create project"); err != nil {
			return err
		}
		if prompt != "" {
			ref.Name = PromptName
			if _, _, err := SaveDocument(ctx, q, ref, KindPrompt, prompt, act.Author(), "create project"); err != nil {
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
	pm, err := parseManifest(raw)
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
		if err := checkLanes(ctx, q, p, pm.m); err != nil {
			return err
		}
		ref := DocRef{Scope: projectScope(p), Name: ManifestName}
		cur, err := q.FindDocument(ctx, ref.find())
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
		if _, n, err = SaveDocument(ctx, q, ref, KindManifest, string(pm.text), act.Author(), reason); err != nil {
			return err
		}
		saved.Revision = n
		for name, mg := range plan {
			if err := applyMigration(ctx, q, store.Time(a.Cfg.Now()), p.ID, pm.m.Collection(name), mg, cur.Current, n); err != nil {
				return err
			}
		}
		if err := keepLegacyDescription(ctx, q, p, pm.legacy, act.Author()); err != nil {
			return err
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

// keepLegacyDescription turns the description of a format-1 manifest into the prompt when there is none.
func keepLegacyDescription(ctx context.Context, q *db.Queries, p db.Project, legacy, author string) error {
	if legacy == "" {
		return nil
	}
	prompt, err := currentText(ctx, q, projectScope(p), KindPrompt, PromptName)
	if err != nil || prompt != "" {
		return err
	}
	_, _, err = SaveDocument(ctx, q, DocRef{Scope: projectScope(p), Name: PromptName}, KindPrompt, legacy, author, "description moved from the manifest")
	return err
}

// UpgradeManifests raises stored manifests of an older format to the current one as new revisions; an
// upgrade that does not pass the checks is not stored.
func (a *App) UpgradeManifests(ctx context.Context) error {
	ps, err := a.Store.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			raw, err := currentText(ctx, q, projectScope(p), KindManifest, ManifestName)
			if err != nil {
				return err
			}
			up, changed, err := manifest.Upgrade([]byte(raw))
			if err != nil || !changed {
				return err
			}
			if _, err := manifest.Parse(up); err != nil {
				return err
			}
			reason := fmt.Sprintf("manifest format upgraded to version %d", manifest.FormatVersion)
			if _, _, err := SaveDocument(ctx, q, DocRef{Scope: projectScope(p), Name: ManifestName}, KindManifest, string(up), "deck", reason); err != nil {
				return err
			}
			return keepLegacyDescription(ctx, q, p, manifest.LegacyDescription([]byte(raw)), "deck")
		})
		if err != nil {
			slog.Error("upgrade manifest; keeping the stored revision", "project", p.Name, "err", err)
		}
	}
	return nil
}

// Reconcile brings stored triggers in line with the current manifests; run it on start.
func (a *App) Reconcile(ctx context.Context) error {
	ps, err := a.Store.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			m, err := loadManifest(ctx, q, p)
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

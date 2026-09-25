package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const maxProblems = 20

var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// loadManifest parses the current manifest of a project.
func loadManifest(ctx context.Context, q *db.Queries, p db.Project) (*manifest.Manifest, error) {
	raw, err := currentText(ctx, q, projectScope(p), KindManifest, ManifestName)
	if err != nil {
		return nil, err
	}
	return manifest.Parse([]byte(raw))
}

// Manifest is the current manifest of a project, read outside any transaction.
func (a *App) Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return loadManifest(ctx, a.Store.Q, p)
}

// Text is the current text of a project document by name, of any kind; empty when missing.
func (a *App) Text(ctx context.Context, projectID int64, name string) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return currentText(ctx, a.Store.Q, projectScope(p), "", name)
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
	up, _, err := manifest.Upgrade(raw)
	if err != nil {
		return parsed{}, registry.Invalid("deck: %v", err)
	}
	return parsed{m: m, text: up, legacy: manifest.LegacyDescription(raw)}, nil
}

// CreateProject validates the manifest, stores it and the prompt as first revisions and registers triggers.
func (a *App) CreateProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt string) (db.Project, error) {
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
		if _, err := q.GetProjectByName(ctx, name); err == nil {
			return registry.Invalid("deck: project %q already exists", name)
		}
		if err := checkReads(ctx, q, act.Workspace, name, pm.m); err != nil {
			return err
		}
		if p, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: act.Workspace, Name: name, Status: "enabled"}); err != nil {
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
		return a.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil {
		return db.Project{}, err
	}
	a.Bus.Publish("projects")
	return p, nil
}

// SaveManifest checks a manifest against deck and the stored records and saves it with its triggers in one
// transaction, so no record write can slip between the check and the new revision.
func (a *App) SaveManifest(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string) (int64, error) {
	pm, err := parseManifest(raw)
	if err != nil {
		return 0, err
	}
	var n int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := checkReads(ctx, q, p.WorkspaceID, p.Name, pm.m); err != nil {
			return err
		}
		if err := checkRecords(ctx, q, p, pm.m); err != nil {
			return err
		}
		if err := checkLanes(ctx, q, p, pm.m); err != nil {
			return err
		}
		if _, n, err = SaveDocument(ctx, q, DocRef{Scope: projectScope(p), Name: ManifestName}, KindManifest, string(pm.text), act.Author(), reason); err != nil {
			return err
		}
		if err := keepLegacyDescription(ctx, q, p, pm.legacy, act.Author()); err != nil {
			return err
		}
		return a.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil {
		return 0, err
	}
	a.Bus.Publish("projects")
	return n, nil
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

// checkReads verifies the projects and collections a manifest reads.
func checkReads(ctx context.Context, q *db.Queries, workspace int64, name string, m *manifest.Manifest) error {
	for _, r := range m.Reads {
		if r.Project == name {
			return registry.Invalid("deck: reads: a project does not need to read itself")
		}
		p, err := q.GetProjectByName(ctx, r.Project)
		if err != nil || p.WorkspaceID != workspace {
			return registry.Invalid("deck: reads: no project %q", r.Project)
		}
		other, err := loadManifest(ctx, q, p)
		if err != nil {
			return registry.Invalid("deck: reads: project %q has an invalid manifest: %v", r.Project, err)
		}
		for _, c := range r.Collections {
			if other.Collection(c) == nil {
				return registry.Invalid("deck: reads: project %q has no collection %q", r.Project, c)
			}
		}
	}
	return nil
}

// checkRecords refuses a manifest whose collections no longer fit the stored records.
func checkRecords(ctx context.Context, q *db.Queries, p db.Project, next *manifest.Manifest) error {
	counts, err := q.CountRecords(ctx, p.ID)
	if err != nil {
		return err
	}
	old, err := loadManifest(ctx, q, p)
	if err != nil {
		slog.Warn("current manifest does not parse; checking every record", "project", p.Name, "err", err)
		old = nil
	}
	var msgs []string
	for _, cnt := range counts {
		c := next.Collection(cnt.Collection)
		if c == nil {
			msgs = append(msgs, fmt.Sprintf("collection %q is removed but has %d records; delete them first with deck.collection_delete", cnt.Collection, cnt.N))
			continue
		}
		if old != nil && sameShape(old.Collection(cnt.Collection), c) {
			continue
		}
		msg, err := misfits(ctx, q, p.ID, c, cnt.N)
		if err != nil {
			return err
		}
		if msg != "" {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) > 0 {
		return registry.Invalid("deck: %s", strings.Join(msgs, "\n"))
	}
	return nil
}

func sameShape(old, next *manifest.Collection) bool {
	if old == nil {
		return false
	}
	return old.Key == next.Key && compactEqual(old.Schema, next.Schema)
}

func compactEqual(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// misfits describes the records of a collection that the new schema or key would break; "" when all fit.
func misfits(ctx context.Context, q *db.Queries, projectID int64, c *manifest.Collection, total int64) (string, error) {
	recs, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: projectID, Collection: c.Name})
	if err != nil {
		return "", err
	}
	var problems []string
	bad := 0
	keys := map[string]int64{}
	for _, r := range recs {
		var why string
		data, err := manifest.DecodeObject([]byte(r.Data))
		switch {
		case err != nil:
			why = "stored data is not a JSON object"
		case c.Validate(data) != nil:
			why = c.Validate(data).Error()
		default:
			key, err := c.KeyOf(data)
			switch {
			case err != nil:
				why = err.Error()
			case key != r.Key:
				why = fmt.Sprintf("key would change from %q to %q", r.Key, key)
			case keys[key] != 0:
				why = fmt.Sprintf("key %q collides with record %d", key, keys[key])
			default:
				keys[key] = r.ID
			}
		}
		if why == "" {
			continue
		}
		bad++
		if len(problems) < maxProblems {
			problems = append(problems, fmt.Sprintf("  - record %d (key %q): %s", r.ID, r.Key, why))
		}
	}
	if bad == 0 {
		return "", nil
	}
	fix := "add a migration script to this revision"
	if c.Migration != "" {
		fix = "migration scripts are not available yet"
	}
	return fmt.Sprintf("collection %q: %d of %d records do not fit the new schema or key; %s. First problems:\n%s",
		c.Name, bad, total, fix, strings.Join(problems, "\n")), nil
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
	}
	return nil
}

// projectCommands turns the commands of workspace manifests into registry commands.
func (a *App) projectCommands(ctx context.Context, workspace int64, name string) ([]*registry.Command, error) {
	var ps []db.Project
	if name == "" {
		all, err := a.Store.Q.ListWorkspaceProjects(ctx, workspace)
		if err != nil {
			return nil, err
		}
		ps = all
	} else {
		p, err := a.Store.Q.GetProjectByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) || err == nil && p.WorkspaceID != workspace {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	}
	var out []*registry.Command
	for _, p := range ps {
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			slog.Error("project manifest", "project", p.Name, "err", err)
			continue
		}
		for _, c := range m.Commands {
			cmd := &registry.Command{
				Name: p.Name + "." + c.Name, Title: c.Title, Description: c.Description, Class: c.Class,
				Input: c.InputSchema(), Result: c.Result, ProjectID: p.ID, Callers: c.Callable(),
				Handler: a.startHandler(p.ID, m, c.Name),
			}
			if err := registry.Prepare(cmd); err != nil {
				slog.Error("project command", "command", cmd.Name, "err", err)
				continue
			}
			out = append(out, cmd)
		}
	}
	return out, nil
}

// startHandler runs a command of the manifest the registry checked the call against.
func (a *App) startHandler(projectID int64, m *manifest.Manifest, command string) registry.Handler {
	return func(ctx context.Context, _ registry.Actor, raw json.RawMessage) (any, error) {
		input, err := manifest.DecodeObject(raw)
		if err != nil {
			return nil, registry.Invalid("deck: input: %v", err)
		}
		id, err := a.Runner.Start(ctx, projectID, m, command, input)
		if err != nil {
			return nil, err
		}
		return Started{Process: id}, nil
	}
}

type Started struct {
	Process int64 `json:"process"`
}

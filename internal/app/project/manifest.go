package project

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
	"fmt"
	"log/slog"
	"strings"
)

type manifestGetIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
}

// manifestView is the current manifest of a project with its document and revision.
type manifestView struct {
	Project  string `json:"project"`
	Document int64  `json:"document"`
	Revision int64  `json:"revision"`
	Manifest string `json:"manifest"`
	// Warnings are problems the manifest is read with; its next save fails on them.
	Warnings []string `json:"warnings,omitempty"`
}

func (pj *Service) manifestGet(ctx context.Context, act registry.Actor, in manifestGetIn) (manifestView, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return manifestView{}, err
	}
	ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
	d, err := docs.Find(ctx, pj.St.Q, ref)
	if err != nil {
		return manifestView{}, err
	}
	r, err := docs.Revision(ctx, pj.St.Q, d, 0)
	if err != nil {
		return manifestView{}, err
	}
	v := manifestView{Project: p.Name, Document: d.ID, Revision: d.Current, Manifest: r.Text}
	v.Warnings = append(v.Warnings, pj.problems(ctx, p)...)
	if m, err := docs.ManifestAt(ctx, pj.St.Q, p.ID, d.Current); err == nil {
		v.Warnings = append(v.Warnings, pj.unassigned(ctx, p, m)...)
	}
	return v, nil
}

type manifestSaveIn struct {
	Project    string   `type:"project" desc:"Project name"`
	Manifest   string   `type:"json" opt:"" desc:"The whole manifest JSON; leave it out to change parts of the current one with old and new"`
	Old        []string `type:"text[]" opt:"" desc:"Parts of the current manifest as manifest_get gives it to replace, each occurring in it exactly once, as it is or with runs of spaces and line breaks taken as one"`
	New        []string `type:"text[]" opt:"" desc:"The text that replaces each old part, in the same order"`
	Reason     string   `opt:"" desc:"Why it changed"`
	IfRevision int64    `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
}

func (pj *Service) manifestSave(ctx context.Context, act registry.Actor, in manifestSaveIn) (any, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if len(in.Old) > 0 || len(in.New) > 0 {
		if in.Manifest != "" {
			return nil, registry.Invalid("deck: give the whole manifest or old and new parts, not both")
		}
		d, err := docs.Find(ctx, pj.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
		if err != nil {
			return nil, err
		}
		if in.IfRevision != 0 && d.Current != in.IfRevision {
			return nil, registry.Conflict("deck: the manifest of %s is at revision %d now, not %d", p.Name, d.Current, in.IfRevision)
		}
		r, err := docs.Revision(ctx, pj.St.Q, d, 0)
		if err != nil {
			return nil, err
		}
		if in.Manifest, err = docs.Edit(r.Text, in.Old, in.New, true); err != nil {
			return nil, err
		}
		in.IfRevision = d.Current
	}
	if strings.TrimSpace(in.Manifest) == "" {
		return nil, registry.Invalid("deck: give the whole manifest JSON, or old and new parts of the current one")
	}
	return pj.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision)
}

// Commands are the commands of manifests.
func (pj *Service) manifestCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("manifest_get", "Get manifest",
			"Returns the current manifest of a project as text; with fields such as manifest.writes, manifest.commands.<name> or manifest.screens.<name>, only that part of it, as text in the same form.",
			registry.Everyone, pj.manifestGet).On("projects"),
		registry.Def("manifest_save", "Save manifest",
			"Validates a manifest and stores it as a new revision: the whole manifest, or old parts of the current one replaced with new ones.",
			registry.OwnerSide, pj.manifestSave),
	}
}

type parsed struct {
	m    *manifest.Manifest
	text []byte
}

// parseManifest validates a manifest and returns the text to store, upgraded to the current format in the one stored
// form (manifest.Canonical); only the built-in project answers events of deck.
func parseManifest(raw []byte, builtin bool) (parsed, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return parsed{}, registry.Invalid("deck: %v", err)
	}
	if w := m.Warnings(); len(w) > 0 {
		return parsed{}, registry.Invalid("deck: %s", strings.Join(w, "\n"))
	}
	for _, t := range m.Triggers {
		if t.On != "" && !builtin {
			return parsed{}, registry.Invalid("deck: a trigger on %s belongs to the built-in project %s", t.On, master.Name)
		}
	}
	return parsed{m: m, text: []byte(manifest.Canonical(string(m.Text())))}, nil
}

// CreateProject validates the manifest, stores it and the prompt as first revisions and registers triggers.
// The owner's project is enabled; anyone else's is a draft.
func (pj *Service) CreateProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt string) (db.Project, error) {
	status := store.ProjectDraft
	if act.Kind == registry.Owner {
		status = store.ProjectEnabled
	}
	return pj.createProject(ctx, act, name, raw, prompt, status, nil)
}

// createProject stores a new project; more stores further documents in the same transaction before the checks.
func (pj *Service) createProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt, status string,
	more func(ctx context.Context, q *db.Queries, p db.Project) error) (db.Project, error) {
	if !access.ProjectName.MatchString(name) || name == registry.Builtin {
		return db.Project{}, registry.Invalid("deck: project name %q must be lowercase letters, digits and '-', start with a letter, and not be %q", name, registry.Builtin)
	}
	pm, err := parseManifest(raw, false)
	if err != nil {
		return db.Project{}, err
	}
	var p db.Project
	err = pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name}); err == nil {
			return registry.Invalid("deck: project %q already exists", name)
		}
		if err := checkKeys(ctx, q, act.Workspace, pm.m); err != nil {
			return err
		}
		if err := checkReads(ctx, q, act.Workspace, name, pm.m); err != nil {
			return err
		}
		if err := pj.checkToolFit(ctx, q, act.Workspace, 0, pm.m); err != nil {
			return err
		}
		if p, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: act.Workspace, Name: name, Status: status}); err != nil {
			return err
		}
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
		_, n, _, err := docs.Save(ctx, q, ref, docs.KindManifest, string(pm.text), act.Author(), "create project")
		if err != nil {
			return err
		}
		if err := grant(ctx, q, p, n, pm.m); err != nil {
			return err
		}
		if prompt != "" {
			ref.Name = docs.PromptName
			if _, _, _, err := docs.Save(ctx, q, ref, docs.KindPrompt, prompt, act.Author(), "create project"); err != nil {
				return err
			}
		}
		if more != nil {
			if err := more(ctx, q, p); err != nil {
				return err
			}
		}
		return pj.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil {
		return db.Project{}, err
	}
	pj.PrepareLater(p.ID)
	return p, nil
}

// Saved is a stored manifest revision, or the ask it waits on when the change widens the project's rights.
type Saved struct {
	Revision int64    `json:"revision,omitempty"`
	Waiting  int64    `json:"waiting,omitempty"`
	Widens   []string `json:"widens,omitempty"`
	// Screens name the blocks of the project's screens that fail on the data there is now.
	Screens []string `json:"screens,omitempty"`
	// Unused are the keys of its i18n that are no text of the manifest.
	Unused []string `json:"unused_i18n,omitempty"`
	// Warnings are what keeps the saved manifest from working, such as a model command no model takes.
	Warnings []string `json:"warnings,omitempty"`
}

// SaveManifest checks a manifest and saves it with its triggers and migrated records in one transaction; a
// change that widens rights waits for the owner; nonzero ifRevision must still be current.
func (pj *Service) SaveManifest(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (Saved, error) {
	pm, err := parseManifest(raw, p.Builtin == 1)
	if err != nil {
		return Saved{}, err
	}
	plan, err := pj.migrate(ctx, p, pm.m, revertTo)
	if err != nil {
		return Saved{}, err
	}
	// The records are checked against the new schemas outside the writer; the writer checks again only when they
	// changed meanwhile.
	seen, err := pj.St.Q.RecordsMark(ctx, p.ID)
	if err != nil {
		return Saved{}, err
	}
	if err := checkRecords(ctx, pj.St.Q, p, pm.m, plan); err != nil {
		return Saved{}, err
	}
	var saved Saved
	changed := false
	var outdated []db.Ask
	err = pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := checkKeys(ctx, q, p.WorkspaceID, pm.m); err != nil {
			return err
		}
		if err := checkReads(ctx, q, p.WorkspaceID, p.Name, pm.m); err != nil {
			return err
		}
		if err := pj.checkToolFit(ctx, q, p.WorkspaceID, p.ID, pm.m); err != nil {
			return err
		}
		if now, err := q.RecordsMark(ctx, p.ID); err != nil || now != seen {
			if err == nil {
				err = checkRecords(ctx, q, p, pm.m, plan)
			}
			if err != nil {
				return err
			}
		}
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
		cur, err := docs.Find(ctx, q, ref)
		if err != nil {
			return err
		}
		if ifRevision != 0 && cur.Current != ifRevision {
			return registry.Conflict("deck: the manifest of %s is at revision %d now, not %d", p.Name, cur.Current, ifRevision)
		}
		w, err := pj.widens(ctx, q, act, p.ID, pm.m)
		if err != nil || len(w) > 0 {
			if err == nil {
				saved, err = pj.ask(ctx, q, act, p, cur, revertTo, w, reason, pm.text)
			}
			return err
		}
		var n int64
		if _, n, changed, err = docs.Save(ctx, q, ref, docs.KindManifest, string(pm.text), act.Author(), reason); err != nil {
			return err
		}
		if saved.Revision = n; !changed {
			return nil
		}
		if err := grant(ctx, q, p, n, pm.m); err != nil {
			return err
		}
		if outdated, err = outdate(ctx, q, p, n, pj.Now()); err != nil {
			return err
		}
		if err := q.DeleteScreenHealth(ctx, store.ID(p.ID)); err != nil {
			return err
		}
		for name, mg := range plan {
			if err := applyMigration(ctx, q, store.Time(pj.Now()), p.ID, pm.m.Collection(name), mg, cur.Current, n); err != nil {
				return err
			}
		}
		if err = pj.Changed(ctx, q, act, p.ID, cur, n, fmt.Sprintf("%s changed the manifest of %s", act.Author(), p.Name), reason); err != nil {
			return err
		}
		return pj.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil || saved.Waiting != 0 {
		return saved, err
	}
	for _, a := range outdated {
		pj.Gateway.Record(ctx, a, "expired", "the manifest changed after this request")
	}
	if changed {
		pj.Bus.Emit(events.Event{Workspace: p.WorkspaceID, Topic: "projects", Project: p.Name})
		pj.PrepareLater(p.ID)
	}
	saved.Screens = pj.CheckScreens(ctx, act, p, pm.m)
	prompt, _ := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName)
	saved.Unused = pm.m.UnusedWords(docs.Description(prompt))
	saved.Warnings = pj.unassigned(ctx, p, pm.m)
	return saved, nil
}

// grant moves the rights the owner gave to revision n of the manifest: an enabled project's save passed the
// owner's check of what it widens; a draft's only when it widens nothing beyond the grant.
func grant(ctx context.Context, q *db.Queries, p db.Project, n int64, next *manifest.Manifest) error {
	p, err := q.GetProject(ctx, p.ID)
	if err != nil {
		return err
	}
	if p.Status != store.ProjectEnabled {
		prev := &manifest.Manifest{}
		if p.GrantedRevision.Valid {
			if prev, err = docs.ManifestAt(ctx, q, p.ID, p.GrantedRevision.Int64); err != nil {
				return err
			}
		}
		if len(manifest.Widens(prev, next)) > 0 {
			return nil
		}
	}
	return q.GrantProject(ctx, db.GrantProjectParams{GrantedRevision: store.ID(n), ID: p.ID})
}

// Reconcile brings stored triggers in line with the current manifests; run it on start.
func (pj *Service) Reconcile(ctx context.Context) error {
	ps, err := pj.St.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		err := pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			m, err := docs.Manifest(ctx, q, p)
			if err != nil {
				return err
			}
			return pj.Scheduler.Sync(ctx, q, p.ID, m)
		})
		if err != nil {
			slog.Error("reconcile triggers", "project", p.Name, "err", err)
		}
		pj.PrepareLater(p.ID)
	}
	return nil
}

// Save is SaveManifest for a service that takes any result.
func (pj *Service) Save(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (any, error) {
	return pj.SaveManifest(ctx, act, p, raw, reason, revertTo, ifRevision)
}

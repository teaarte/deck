package project

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
	"deck2/master"
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

func (s *Service) manifestGet(ctx context.Context, act registry.Actor, in manifestGetIn) (manifestView, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return manifestView{}, err
	}
	ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
	d, err := docs.Find(ctx, s.St.Q, ref)
	if err != nil {
		return manifestView{}, err
	}
	r, err := docs.Revision(ctx, s.St.Q, d, 0)
	if err != nil {
		return manifestView{}, err
	}
	if gateway.Live(ctx) && act.ProcessID != 0 {
		s.Saw(act.ProcessID, d.ID, d.Current)
	}
	v := manifestView{Project: p.Name, Document: d.ID, Revision: d.Current, Manifest: r.Text}
	v.Warnings = append(v.Warnings, s.problems(ctx, p)...)
	if m, err := docs.ManifestAt(ctx, s.St.Q, p.ID, d.Current); err == nil {
		v.Warnings = append(v.Warnings, s.unassigned(ctx, p, m)...)
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

func (s *Service) manifestSave(ctx context.Context, act registry.Actor, in manifestSaveIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	whole := len(in.Old) == 0 && in.IfRevision == 0
	if err := s.edited(ctx, p, &in); err != nil {
		return nil, err
	}
	d, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
	if err != nil {
		return nil, err
	}
	if whole && gateway.Live(ctx) && act.ProcessID != 0 {
		// A whole manifest of a turn goes only over the revision the turn read.
		base := s.Seen(act.ProcessID, d.ID)
		if err := docs.Since(ctx, s.St.Q, d, base); err != nil {
			return nil, err
		}
		in.IfRevision = base
	}
	saved, err := s.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision)
	if err == nil && gateway.Live(ctx) && act.ProcessID != 0 && saved.Revision != 0 {
		s.Saw(act.ProcessID, d.ID, saved.Revision)
	}
	return saved, err
}

// edited puts the whole manifest a save gives in in.Manifest: as it came, or the current one with its old parts
// replaced by the new, pinned to the revision it was read at.
func (s *Service) edited(ctx context.Context, p db.Project, in *manifestSaveIn) error {
	if len(in.Old) > 0 || len(in.New) > 0 {
		if in.Manifest != "" {
			return registry.Invalid("give the whole manifest or old and new parts, not both")
		}
		d, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
		if err != nil {
			return err
		}
		if in.IfRevision != 0 && d.Current != in.IfRevision {
			return registry.Conflict("the manifest of %s is at revision %d now, not %d", p.Name, d.Current, in.IfRevision)
		}
		r, err := docs.Revision(ctx, s.St.Q, d, 0)
		if err != nil {
			return err
		}
		if in.Manifest, err = docs.Edit(r.Text, in.Old, in.New, true); err != nil {
			return err
		}
		in.IfRevision = d.Current
	}
	if strings.TrimSpace(in.Manifest) == "" {
		return registry.Invalid("give the whole manifest JSON, or old and new parts of the current one")
	}
	return nil
}

// Commands are the commands of manifests.
func (s *Service) manifestCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("manifest_get", "Get manifest",
			"Returns the current manifest of a project as text; with fields such as manifest.writes, manifest.commands.<name> or manifest.screens.<name>, only that part of it, as text in the same form.",
			registry.Everyone, s.manifestGet).On(events.Projects),
		registry.Def("manifest_save", "Save manifest",
			"Validates a manifest and stores it as a new revision: the whole manifest, or old parts of the current one replaced with new ones.",
			registry.OwnerSide, s.manifestSave),
	}
}

type parsed struct {
	m    *manifest.Manifest
	text []byte
}

// parseManifest validates a manifest and returns the text to store, upgraded to the current format in the one stored
// form (manifest.Canonical); only the built-in project answers events of deck other than an ask.
func parseManifest(raw []byte, builtin bool) (parsed, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return parsed{}, registry.Invalid("%v", err)
	}
	if w := m.Warnings(); len(w) > 0 {
		return parsed{}, registry.Invalid("%s", strings.Join(w, "\n"))
	}
	for _, t := range m.Triggers {
		if t.On != "" && t.On != manifest.OnAsk && !builtin {
			return parsed{}, registry.Invalid("a trigger on %s belongs to the built-in project %s", t.On, master.Name)
		}
		if c := m.Command(t.Command); t.On == manifest.OnAsk && c != nil && c.Class == registry.External {
			return parsed{}, registry.Invalid("a trigger on ask starts a command without the owner, so not %s, which is external", t.Command)
		}
	}
	return parsed{m: m, text: []byte(manifest.Canonical(string(m.Text())))}, nil
}

// Saved is a stored manifest revision, or the ask it waits on when the change widens the project's rights.
type Saved struct {
	Revision int64    `json:"revision,omitempty"`
	Waiting  int64    `json:"waiting,omitempty"`
	Widens   []string `json:"widens,omitempty"`
	// Screens name the blocks of the project's screens that fail on the data there is now.
	Screens []string `json:"screens,omitempty"`
	// Unused are the keys of its i18n that are no text of the manifest; Untranslated the texts a language of it lacks.
	Unused       []string `json:"unused_i18n,omitempty"`
	Untranslated []string `json:"untranslated,omitempty"`
	// Warnings are what keeps the saved manifest from working, such as a model command no model takes.
	Warnings []string `json:"warnings,omitempty"`
}

// SaveManifest checks a manifest and saves it with its triggers and migrated records in one transaction; a
// change that widens rights waits for the owner; nonzero ifRevision must still be current.
func (s *Service) SaveManifest(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (Saved, error) {
	pm, err := parseManifest(raw, p.Builtin == 1)
	if err != nil {
		return Saved{}, err
	}
	plan, err := s.migrate(ctx, p, pm.m, revertTo, false)
	if err != nil {
		return Saved{}, err
	}
	// The records are checked against the new schemas outside the writer; the writer checks again only when they
	// changed meanwhile.
	seen, err := s.St.Q.RecordsMark(ctx, p.ID)
	if err != nil {
		return Saved{}, err
	}
	if err := checkRecords(ctx, s.St.Q, p, pm.m, plan); err != nil {
		return Saved{}, err
	}
	var saved Saved
	changed := false
	var outdated []db.Ask
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := checkReads(ctx, q, p.WorkspaceID, p.Name, pm.m); err != nil {
			return err
		}
		if err := s.checkToolFit(ctx, q, p.WorkspaceID, p.ID, pm.m); err != nil {
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
			return registry.Conflict("the manifest of %s is at revision %d now, not %d", p.Name, cur.Current, ifRevision)
		}
		w, err := s.widens(ctx, q, act, p.ID, pm.m)
		if err != nil || len(w) > 0 {
			if err == nil {
				saved, err = s.ask(ctx, q, act, p, cur, revertTo, w, reason, pm.text)
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
		if outdated, err = outdate(ctx, q, p, n, s.Now()); err != nil {
			return err
		}
		if err := q.DeleteScreenHealth(ctx, db.DeleteScreenHealthParams{ProjectID: store.ID(p.ID), Subjects: health.OfScreen + ":*"}); err != nil {
			return err
		}
		for name, mg := range plan {
			if err := applyMigration(ctx, q, store.Time(s.Now()), p.ID, pm.m.Collection(name), mg, cur.Current, n); err != nil {
				return err
			}
		}
		if err = s.Noted(ctx, q, act, p.ID, cur, n, fmt.Sprintf("%s changed the manifest of %s", act.Author(), p.Name), fmt.Sprintf("changed the manifest of %s", p.Name), reason); err != nil {
			return err
		}
		return s.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil || saved.Waiting != 0 {
		return saved, err
	}
	for _, a := range outdated {
		s.Gateway.Record(ctx, a, "expired", "the manifest changed after this request")
	}
	if changed {
		s.Emit(events.Event{Workspace: p.WorkspaceID, Topic: events.Projects, Project: p.Name})
		s.PrepareLater(p.ID)
	}
	saved.Screens = s.CheckScreens(ctx, act, p, pm.m)
	saved.Unused, saved.Untranslated = s.words(ctx, p, pm.m)
	saved.Warnings = slices.Concat(s.unassigned(ctx, p, pm.m), s.unpaged(ctx, p, pm.m), unreported(p, pm.m))
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
func (s *Service) Reconcile(ctx context.Context) error {
	ps, err := s.St.Q.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			m, err := docs.Manifest(ctx, q, p)
			if err != nil {
				return err
			}
			return s.Scheduler.Sync(ctx, q, p.ID, m)
		})
		if err != nil {
			slog.Error("reconcile triggers", "project", p.Name, "err", err)
		}
		s.PrepareLater(p.ID)
	}
	return nil
}

// Save is SaveManifest for a service that takes any result.
func (s *Service) Save(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (any, error) {
	return s.SaveManifest(ctx, act, p, raw, reason, revertTo, ifRevision)
}

// words are the keys of the i18n of a manifest of p that nothing uses and the texts it does not translate; data
// wants no translation.
func (s *Service) words(ctx context.Context, p db.Project, m *manifest.Manifest) (unused, untranslated []string) {
	var also, values []string
	if p.ID != 0 {
		prompt, _ := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName)
		also, values = []string{docs.Description(prompt)}, s.filterValues(ctx, p.ID, m)
	}
	unused = m.UnusedWords(also, values)
	untranslated = m.Untranslated(func(lang, text string) bool { return words.In(lang)[text] != "" }, also...)
	return unused, untranslated
}

// filterValues are the values the records of a project hold in the fields its screens filter by, which a filter
// shows in the words of the manifest where it has them.
func (s *Service) filterValues(ctx context.Context, project int64, m *manifest.Manifest) []string {
	var out []string
	for col, fields := range m.Filtered() {
		for _, f := range fields {
			vs, err := s.St.Q.FieldValues(ctx, db.FieldValuesParams{Path: `$."` + f + `"`, ProjectID: project, Collection: col, Lim: 100})
			if err == nil {
				out = append(out, vs...)
			}
		}
	}
	return out
}

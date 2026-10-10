package project

import (
	"context"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
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

func (sv *Saver) manifestGet(ctx context.Context, act registry.Actor, in manifestGetIn) (manifestView, error) {
	p, err := access.Project(ctx, sv.St.Q, act, in.Project)
	if err != nil {
		return manifestView{}, err
	}
	ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
	d, err := docs.Find(ctx, sv.St.Q, ref)
	if err != nil {
		return manifestView{}, err
	}
	r, err := docs.Revision(ctx, sv.St.Q, d, 0)
	if err != nil {
		return manifestView{}, err
	}
	v := manifestView{Project: p.Name, Document: d.ID, Revision: d.Current, Manifest: r.Text}
	if m, err := docs.ManifestAt(ctx, sv.St.Q, p.ID, d.Current); err == nil {
		v.Warnings = m.Warnings()
		held, err := heldKeys(ctx, sv.St.Q, p.WorkspaceID, m)
		if err != nil {
			return manifestView{}, err
		}
		v.Warnings = append(v.Warnings, held...)
	}
	return v, nil
}

type manifestSaveIn struct {
	Project    string `type:"project" desc:"Project name"`
	Manifest   string `type:"json" opt:"" desc:"The whole manifest JSON; leave it out to change the current one with patch"`
	Patch      string `type:"json" opt:"" desc:"JSON Patch (RFC 6902) of the current manifest, such as [{\"op\": \"replace\", \"path\": \"/screens/0/title\", \"value\": \"Overview\"}]: paths point at one place, so equal texts elsewhere stay"`
	Reason     string `opt:"" desc:"Why it changed"`
	IfRevision int64  `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
}

func (sv *Saver) manifestSave(ctx context.Context, act registry.Actor, in manifestSaveIn) (any, error) {
	p, err := access.Project(ctx, sv.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Patch) != "" {
		if in.Manifest != "" {
			return nil, registry.Invalid("deck: give the whole manifest or a patch, not both")
		}
		d, err := docs.Find(ctx, sv.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
		if err != nil {
			return nil, err
		}
		if in.IfRevision != 0 && d.Current != in.IfRevision {
			return nil, registry.Conflict("deck: the manifest of %s is at revision %d now, not %d", p.Name, d.Current, in.IfRevision)
		}
		r, err := docs.Revision(ctx, sv.St.Q, d, 0)
		if err != nil {
			return nil, err
		}
		if in.Manifest, err = docs.Patch(r.Text, in.Patch); err != nil {
			return nil, err
		}
		in.IfRevision = d.Current
	}
	if strings.TrimSpace(in.Manifest) == "" {
		return nil, registry.Invalid("deck: give the whole manifest JSON or a JSON Patch of the current one")
	}
	return sv.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision,
		Call{Command: registry.Builtin + ".manifest_save", Input: map[string]any{"project": p.Name, "manifest": in.Manifest, "reason": in.Reason}})
}

// Commands are the commands of manifests.
func (sv *Saver) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("manifest_get", "Get manifest",
			"Returns the current manifest of a project.",
			registry.Everyone, sv.manifestGet).On("projects"),
		registry.Def("manifest_save", "Save manifest",
			"Validates a manifest and stores it as a new revision: the whole manifest, or a JSON Patch of the current one; a replace keeps the text around it as written.",
			registry.OwnerSide, sv.manifestSave),
	}
}

package project

import (
	"context"
	"fmt"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type manifestCheckIn struct {
	Project  string   `type:"project" opt:"" desc:"The project whose manifest it would be; without one the manifest is checked alone"`
	Manifest string   `type:"json" opt:"" desc:"The whole manifest JSON; leave it out to check old parts of the current one replaced with new ones, or with neither the current one"`
	Old      []string `type:"text[]" opt:"" desc:"Parts of the current manifest to replace, as for manifest_save"`
	New      []string `type:"text[]" opt:"" desc:"The text that replaces each old part, in the same order"`
}

// checked is what a save of a manifest would say, without the save.
type checked struct {
	// Ok says a save would take it; Refused are why it would not.
	Ok      bool     `json:"ok"`
	Refused []string `json:"refused"`
	// Warnings are what keeps it from working though a save takes it, such as a command no model takes.
	Warnings []string `json:"warnings"`
	// Widens are the rights it adds to those in force; Waits says a save by this caller waits for the owner's yes,
	// Enable that the project is a draft whose rights come when the owner enables it.
	Widens []string `json:"widens"`
	Waits  bool     `json:"waits"`
	Enable bool     `json:"enable,omitempty"`
	// Screens name the blocks of its screens that fail on the data there is now.
	Screens      []string `json:"screens"`
	Unused       []string `json:"unused_i18n"`
	Untranslated []string `json:"untranslated"`
}

// check runs the checks of a save on a manifest and writes nothing; a migration script it would run it only names.
func (pj *Service) check(ctx context.Context, act registry.Actor, in manifestCheckIn) (checked, error) {
	out := checked{Refused: []string{}, Warnings: []string{}, Widens: []string{}, Screens: []string{}, Unused: []string{}, Untranslated: []string{}}
	var p db.Project
	if in.Project != "" {
		var err error
		if p, err = access.Project(ctx, pj.St.Q, act, in.Project); err != nil {
			return out, err
		}
		if strings.TrimSpace(in.Manifest) == "" && len(in.Old)+len(in.New) == 0 {
			cur, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindManifest, docs.ManifestName)
			if err != nil {
				return out, err
			}
			in.Manifest = cur
		} else {
			save := manifestSaveIn{Manifest: in.Manifest, Old: in.Old, New: in.New}
			if err := pj.edited(ctx, p, &save); err != nil {
				return out, err
			}
			in.Manifest = save.Manifest
		}
	} else if strings.TrimSpace(in.Manifest) == "" {
		return out, registry.Invalid("deck: give the manifest JSON, or a project and old and new parts of its manifest")
	}
	pm, err := parseManifest([]byte(in.Manifest), p.Builtin == 1)
	if err != nil {
		out.Refused = refusals(err)
		return out, nil
	}
	for _, err := range []error{checkKeys(ctx, pj.St.Q, act.Workspace, pm.m), checkReads(ctx, pj.St.Q, act.Workspace, p.Name, pm.m)} {
		out.Refused = append(out.Refused, refusals(err)...)
	}
	prev := &manifest.Manifest{}
	if p.ID != 0 {
		out.Refused = append(out.Refused, refusals(pj.checkToolFit(ctx, pj.St.Q, act.Workspace, p.ID, pm.m))...)
		if plan, err := pj.migrate(ctx, p, pm.m, 0, true); err != nil {
			out.Refused = append(out.Refused, refusals(err)...)
		} else {
			out.Refused = append(out.Refused, refusals(checkRecords(ctx, pj.St.Q, p, pm.m, plan))...)
			for name, mg := range plan {
				if mg.checked {
					out.Warnings = append(out.Warnings, fmt.Sprintf("a save runs the migration script %s of collection %q; its records are checked then", pm.m.Collection(name).Migration, name))
				}
			}
		}
		if cur, err := docs.Manifest(ctx, pj.St.Q, p); err == nil {
			if prev, err = docs.InForce(ctx, pj.St.Q, p, cur); err != nil {
				prev = &manifest.Manifest{}
			}
		}
		out.Screens = append(out.Screens, pj.CheckScreens(ctx, act, p, pm.m)...)
		out.Warnings = append(append(out.Warnings, pj.unassigned(ctx, p, pm.m)...), pj.unpaged(ctx, p, pm.m)...)
		out.Enable = p.Status != store.ProjectEnabled
	}
	out.Widens = append(out.Widens, manifest.Widens(prev, pm.m)...)
	out.Waits = len(out.Widens) > 0 && act.Kind != registry.Owner && !out.Enable
	unused, untranslated := pj.words(ctx, p, pm.m)
	out.Unused = append(out.Unused, unused...)
	out.Untranslated = append(out.Untranslated, untranslated...)
	out.Ok = len(out.Refused) == 0
	return out, nil
}

package project

import (
	"context"
	"deck2/internal/events"
	"fmt"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// draftView is a draft on one card: what it does, what enabling it lets it do, the models it takes and what it still
// lacks, in lines of markdown, with the revision its enable grants.
type draftView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description"`
	Revision    int64  `json:"revision"`
	Draft       bool   `json:"draft"`
	Rights      string `json:"rights,omitempty"`
	Models      string `json:"models,omitempty"`
	Lacks       string `json:"lacks,omitempty"`
	// Secrets says some secret it names is not set yet.
	Secrets bool `json:"secrets"`
}

type projectDraftGetIn struct {
	Project string `type:"project" desc:"Project name"`
}

// projectDraft is the card of a draft, a list of one as the blocks of a screen read it.
func (s *Service) projectDraft(ctx context.Context, act registry.Actor, in projectDraftGetIn) ([]draftView, error) {
	v, err := s.draftOf(ctx, act, in)
	if err != nil {
		return nil, err
	}
	return []draftView{v}, nil
}

func (s *Service) draftOf(ctx context.Context, act registry.Actor, in projectDraftGetIn) (draftView, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return draftView{}, err
	}
	ms, failed := docs.All(ctx, s.St.Q, []db.Project{p})
	if err := failed[p.ID]; err != nil {
		return draftView{}, registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
	}
	ws := words.In(registry.Lang(ctx))
	tr := func(t string) string { return manifest.TrText(ws, t) }
	v := s.viewProject(ctx, p, ms[p.ID], nil, registry.Lang(ctx))
	out := draftView{ID: p.ID, Name: p.Name, Title: v.Title, Icon: v.Icon, Description: v.Description, Revision: v.Revision, Draft: p.Status == "draft"}
	gs, err := s.St.Q.ListGrants(ctx, p.ID)
	if err != nil {
		return out, err
	}
	lines := lines(rows(p, ms[p.ID], gs, func(t string) string { return manifest.Tr(ws, t) }, nil), tr)
	// A custom block holds what its screen gives it, and deck cannot keep every way out of a browser shut.
	if cs, err := s.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Kind: docs.KindCustom}); err == nil && len(cs) > 0 {
		lines = append(lines, "- "+tr("its custom blocks see the data their screens give them and may carry it out of deck"))
	}
	out.Rights = strings.Join(lines, "\n")
	if len(v.Models) > 0 {
		out.Models = "- " + strings.Join(v.Models, "\n- ")
	}
	lines = nil
	for _, n := range s.needs(ctx, p, ms[p.ID], nil, ws) {
		if n.Done || n.Optional {
			continue
		}
		if n.Kind == "secret" {
			out.Secrets = true
			lines = append(lines, "- "+bullet(fmt.Sprintf(tr("secret %s"), n.What), ""))
			continue
		}
		lines = append(lines, "- "+bullet(n.What, n.Detail))
	}
	out.Lacks = strings.Join(lines, "\n")
	return out, nil
}

// bullet is a line of a list: what, and in a quieter voice why.
func bullet(what, detail string) string {
	if detail == "" {
		return what
	}
	return what + " — " + detail
}

func (s *Service) draftCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("project_draft_list", "A draft on one card",
			"Gives a module on one card to enable it: what it does, what enabling it lets it do, the models it takes, what it still lacks and the revision enabling grants.",
			registry.OwnerSide, s.projectDraft).On(events.Projects, events.Documents, events.Secrets, events.Models, events.Assignments),
	}
}

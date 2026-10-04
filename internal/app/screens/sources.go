package screens

import (
	"context"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Sources read what the blocks of screens show besides the commands they read: boards, chats, forms and documents.
type Sources struct {
	Store     *store.Store
	Registry  *registry.Registry
	Manifests *docs.Manifests
	Deck      Deck
}

func (s Sources) Board(ctx context.Context, p db.Project, m *manifest.Manifest) (any, error) {
	cards, err := s.Store.Q.ListCards(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	waits, err := s.Deck.Waits(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	lanes := []map[string]any{}
	for _, l := range m.Board.Lanes {
		shown := []map[string]any{}
		for _, c := range cards {
			if c.Lane != l.Name {
				continue
			}
			card := map[string]any{"id": c.ID, "title": c.Title, "text": c.Text, "waits": append([]int64{}, waits[c.ID]...)}
			if c.RecordID.Int64 != 0 {
				card["record"] = c.RecordID.Int64
			}
			shown = append(shown, card)
		}
		lanes = append(lanes, map[string]any{"name": l.Name, "title": l.Title, "role": l.Role, "cards": shown})
	}
	deleted, err := s.Store.Q.ListDeletedCards(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	gone := []map[string]any{}
	for _, c := range deleted {
		gone = append(gone, map[string]any{"id": c.ID, "title": c.Title})
	}
	others := []string{}
	ps, err := s.Store.Q.ListWorkspaceProjects(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	ms, _ := s.Manifests.All(ctx, ps)
	for _, o := range ps {
		if om := ms[o.ID]; o.ID != p.ID && om != nil && om.RoleLane("inbox") != nil {
			others = append(others, o.Name)
		}
	}
	return map[string]any{"project": p.Name, "lanes": lanes, "deleted": gone, "projects": others}, nil
}

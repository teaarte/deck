package screens

import (
	"context"
	"fmt"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Sources fill the blocks of deck's screens and of project screens with rows.
type Sources struct {
	Store    *store.Store
	Registry *registry.Registry
	Deck     Deck
	Now      func() time.Time
	LogPath  func(process int64) string
}

func (s Sources) Items(ctx context.Context, from string, sc screen.Scope) ([]map[string]any, error) {
	names, err := ProjectNames(ctx, s.Store.Q, sc.Workspace)
	if err != nil {
		return nil, err
	}
	switch from {
	case "asks":
		return s.askItems(ctx, sc, names)
	case "processes":
		return s.processItems(ctx, sc, names)
	case "unattended":
		return s.unattendedItems(ctx, sc, names)
	case "providers":
		return s.providerItems(ctx, sc)
	case "log":
		return s.logItems(sc.Record)
	case "triggers":
		return s.triggerItems(ctx, sc)
	case "projects":
		return s.projectItems(ctx, sc)
	case "notifications":
		return s.notificationItems(ctx, sc, names)
	case "secrets":
		return s.secretItems(ctx, sc)
	case "tokens":
		return s.tokenItems(ctx, sc)
	case "health":
		return s.healthItems(ctx, sc)
	case "settings":
		return s.Deck.Settings(ctx, sc.Workspace), nil
	case "documents":
		return s.documentItems(ctx, sc, names)
	case "calls":
		return s.Deck.Calls(ctx, sc.Workspace, filterProject(sc, names), sc.Filter["outcome"])
	case "spend":
		return s.spendItems(ctx, sc, names)
	case "files":
		return s.fileItems(ctx, sc, names)
	case "snapshots":
		return s.Deck.Snapshots()
	case "commands":
		return s.commandItems(ctx, sc, names)
	}
	return nil, fmt.Errorf("unknown source %q", from)
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
	return map[string]any{"project": p.Name, "lanes": lanes, "deleted": gone}, nil
}

// ProjectNames are the names of the projects of a workspace by id.
func ProjectNames(ctx context.Context, q *db.Queries, workspace int64) (map[int64]string, error) {
	ps, err := q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, p := range ps {
		out[p.ID] = p.Name
	}
	return out, nil
}

// filterProject is the project a filter picks by name within the scope, -1 for none, the scope without a name.
func filterProject(sc screen.Scope, names map[int64]string) int64 {
	name := sc.Filter["project"]
	if name == "" {
		return sc.Project
	}
	for id, n := range names {
		if n == name && (sc.Project == 0 || id == sc.Project) {
			return id
		}
	}
	return -1
}

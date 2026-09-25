package app

import (
	"context"
	"database/sql"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// boardManifest is the manifest of a project that has a board.
func boardManifest(ctx context.Context, q *db.Queries, p db.Project) (*manifest.Manifest, error) {
	m, err := loadManifest(ctx, q, p)
	if err != nil {
		return nil, err
	}
	if m.Board == nil {
		return nil, registry.Invalid("deck: project %q has no board", p.Name)
	}
	return m, nil
}

// landing is where a card goes without a lane: the inbox lane, else the first one.
func landing(m *manifest.Manifest) string {
	if l := m.RoleLane("inbox"); l != nil {
		return l.Name
	}
	return m.Board.Lanes[0].Name
}

// boardWrite is one card write: the project, its manifest, and the cards that entered a lane.
type boardWrite struct {
	p       db.Project
	m       *manifest.Manifest
	entered []entry
	// parent is the process of the agent that moved the cards; the lane commands continue its chain.
	parent int64
}

type entry struct {
	card int64
	lane string
}

func (w *boardWrite) enter(card int64, lane string) { w.entered = append(w.entered, entry{card, lane}) }

// writeBoard runs a card write in one transaction, then starts the lane triggers of the lanes cards entered.
func (a *App) writeBoard(ctx context.Context, act registry.Actor, f func(ctx context.Context, q *db.Queries, w *boardWrite) error) error {
	w := &boardWrite{parent: act.ProcessID}
	if err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return f(ctx, q, w) }); err != nil {
		return err
	}
	a.Bus.Publish("board")
	for _, e := range w.entered {
		a.laneTriggers(ctx, w.p, w.m, e.card, e.lane, w.parent)
	}
	return nil
}

// laneTriggers starts the commands of a lane for a card that entered it and links the last process to the
// card; while a card's run is still going, the card entering the lane again does not start another.
func (a *App) laneTriggers(ctx context.Context, p db.Project, m *manifest.Manifest, card int64, lane string, parent int64) {
	linked := false
	defer func() {
		if linked {
			a.Bus.Publish("board")
		}
	}()
	if p.Paused == 1 {
		return
	}
	rows, err := a.Store.Q.ListProjectTriggers(ctx, p.ID)
	if err != nil {
		slog.Error("lane triggers", "project", p.Name, "err", err)
		return
	}
	for _, t := range m.Triggers {
		if t.Lane != lane {
			continue
		}
		row, ok := trigger.Find(rows, t)
		if !ok || row.Paused == 1 {
			continue
		}
		input := manifest.TriggerInput(t, card)
		input[manifest.RetryKey] = fmt.Sprintf("trigger:%d:card:%d", row.ID, card)
		id, err := a.fire(ctx, row, input, parent)
		if err != nil {
			slog.Error("lane trigger", "project", p.Name, "command", t.Command, "card", card, "err", err)
			continue
		}
		err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetCardProcess(ctx, db.SetCardProcessParams{ProcessID: sql.NullInt64{Int64: id, Valid: true}, ID: card})
		})
		if err != nil {
			slog.Error("link card process", "card", card, "err", err)
		}
		linked = linked || err == nil
	}
}

// place puts a card into a lane before another card, or last, and renumbers the lane.
func place(ctx context.Context, q *db.Queries, c db.Card, lane string, before int64) error {
	rows, err := q.ListLaneCards(ctx, db.ListLaneCardsParams{ProjectID: c.ProjectID, Lane: lane})
	if err != nil {
		return err
	}
	rows = slices.DeleteFunc(rows, func(r db.ListLaneCardsRow) bool { return r.ID == c.ID })
	at := len(rows)
	if before != 0 {
		if before == c.ID {
			return registry.Invalid("deck: a card cannot be placed before itself")
		}
		if at = slices.IndexFunc(rows, func(r db.ListLaneCardsRow) bool { return r.ID == before }); at < 0 {
			return registry.Invalid("deck: card %d is not in lane %q", before, lane)
		}
	}
	rows = slices.Insert(rows, at, db.ListLaneCardsRow{ID: c.ID, Rank: -1})
	for i, r := range rows {
		rank := int64(i + 1)
		switch {
		case r.ID == c.ID:
			err = q.PlaceCard(ctx, db.PlaceCardParams{Lane: lane, Rank: rank, ID: c.ID})
		case r.Rank != rank:
			err = q.SetCardRank(ctx, db.SetCardRankParams{Rank: rank, ID: r.ID})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// setWaits replaces the live cards a card waits for, refusing other projects, deleted cards and cycles;
// waits on deleted cards stay so a restore brings them back.
func setWaits(ctx context.Context, q *db.Queries, c db.Card, waits []int64) error {
	graph, err := edgesOf(ctx, q, c.ProjectID, true)
	if err != nil {
		return err
	}
	live, err := waitsOf(ctx, q, c.ProjectID)
	if err != nil {
		return err
	}
	graph[c.ID] = slices.DeleteFunc(graph[c.ID], func(w int64) bool { return slices.Contains(live[c.ID], w) })
	for _, w := range waits {
		if slices.Contains(graph[c.ID], w) {
			continue
		}
		if w == c.ID {
			return registry.Invalid("deck: a card cannot wait for itself")
		}
		o, err := q.GetCard(ctx, w)
		if errors.Is(err, sql.ErrNoRows) || err == nil && o.ProjectID != c.ProjectID {
			return registry.NotFound("deck: no card %d in this project", w)
		}
		if err != nil {
			return err
		}
		if err := liveCard(o); err != nil {
			return err
		}
		if !slices.Contains(graph[c.ID], w) {
			graph[c.ID] = append(graph[c.ID], w)
		}
	}
	for _, w := range graph[c.ID] {
		if path := waitPath(graph, w, c.ID); path != nil {
			steps := make([]string, 0, len(path)+1)
			for _, id := range append([]int64{c.ID}, path...) {
				steps = append(steps, fmt.Sprintf("#%d", id))
			}
			return registry.Invalid("deck: waiting would make a cycle: %s", strings.Join(steps, " waits for "))
		}
	}
	if err := q.DeleteCardEdges(ctx, c.ID); err != nil {
		return err
	}
	for _, w := range graph[c.ID] {
		if err := q.AddCardEdge(ctx, db.AddCardEdgeParams{CardID: c.ID, WaitsFor: w}); err != nil {
			return err
		}
	}
	return nil
}

// waitPath returns the chain of waits from one card to another, or nil.
func waitPath(graph map[int64][]int64, from, to int64) []int64 {
	seen := map[int64]bool{}
	var walk func(n int64) []int64
	walk = func(n int64) []int64 {
		if n == to {
			return []int64{n}
		}
		if seen[n] {
			return nil
		}
		seen[n] = true
		for _, next := range graph[n] {
			if p := walk(next); p != nil {
				return append([]int64{n}, p...)
			}
		}
		return nil
	}
	return walk(from)
}

// checkLanes refuses a manifest that drops a lane still holding cards.
func checkLanes(ctx context.Context, q *db.Queries, p db.Project, next *manifest.Manifest) error {
	counts, err := q.CountLaneCards(ctx, p.ID)
	if err != nil {
		return err
	}
	var msgs []string
	for _, c := range counts {
		if next.Lane(c.Lane) == nil {
			msgs = append(msgs, fmt.Sprintf("lane %q is removed but has %d cards; move them to another lane first, or change only its title", c.Lane, c.N))
		}
	}
	if len(msgs) > 0 {
		return registry.Invalid("deck: %s", strings.Join(msgs, "\n"))
	}
	return nil
}

// purgeCards removes cards deleted longer ago than cardRetention, with their documents.
func (a *App) purgeCards(ctx context.Context, now time.Time) {
	before := now.Add(-cardRetention).UTC().Format("2006-01-02T15:04:05.000Z")
	var n int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.DeleteExpiredCardDocuments(ctx, sql.NullString{String: before, Valid: true}); err != nil {
			return err
		}
		var err error
		n, err = q.PurgeCards(ctx, sql.NullString{String: before, Valid: true})
		return err
	})
	if err != nil {
		slog.Error("purge cards", "err", err)
		return
	}
	if n > 0 {
		a.Bus.Publish("board")
	}
}

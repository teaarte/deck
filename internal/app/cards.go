package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// cardRetention is how long a deleted card can be restored before the sweep removes it.
const cardRetention = 24 * time.Hour

type cardView struct {
	ID        int64   `json:"id"`
	Project   int64   `json:"project"`
	Title     string  `json:"title"`
	Text      string  `json:"text"`
	Lane      string  `json:"lane"`
	Rank      int64   `json:"rank"`
	Record    int64   `json:"record,omitempty"`
	Report    string  `json:"report,omitempty"`
	Process   int64   `json:"process,omitempty"`
	Waits     []int64 `json:"waits"`
	DeletedAt string  `json:"deleted_at,omitempty"`
	UpdatedAt string  `json:"updated_at"`
}

type laneView struct {
	Name  string     `json:"name"`
	Title string     `json:"title"`
	Role  string     `json:"role,omitempty"`
	Cards []cardView `json:"cards"`
}

type boardView struct {
	Project string     `json:"project"`
	Runs    bool       `json:"runs"`
	Lanes   []laneView `json:"lanes"`
	Deleted []cardView `json:"deleted,omitempty"`
}

func viewCard(c db.Card, waits map[int64][]int64) cardView {
	return cardView{ID: c.ID, Project: c.ProjectID, Title: c.Title, Text: c.Text, Lane: c.Lane, Rank: c.Rank,
		Record: c.RecordID.Int64, Report: c.Report, Process: c.ProcessID.Int64, Waits: append([]int64{}, waits[c.ID]...),
		DeletedAt: c.DeletedAt.String, UpdatedAt: c.UpdatedAt}
}

// waitsOf maps each card of a project to the live cards it waits for.
func waitsOf(ctx context.Context, q *db.Queries, projectID int64) (map[int64][]int64, error) {
	return edgesOf(ctx, q, projectID, false)
}

// edgesOf maps cards to the cards they wait for; waits on deleted cards only when withDeleted is set.
func edgesOf(ctx context.Context, q *db.Queries, projectID int64, withDeleted bool) (map[int64][]int64, error) {
	edges, err := q.ListProjectEdges(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, e := range edges {
		if e.Gone == 0 || withDeleted {
			out[e.CardID] = append(out[e.CardID], e.WaitsFor)
		}
	}
	return out, nil
}

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
}

type entry struct {
	card int64
	lane string
}

func (w *boardWrite) enter(card int64, lane string) { w.entered = append(w.entered, entry{card, lane}) }

// writeBoard runs a card write in one transaction, then starts the lane triggers of the lanes cards entered.
func (a *App) writeBoard(ctx context.Context, f func(ctx context.Context, q *db.Queries, w *boardWrite) error) error {
	w := &boardWrite{}
	if err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return f(ctx, q, w) }); err != nil {
		return err
	}
	a.Bus.Publish("board")
	for _, e := range w.entered {
		a.laneTriggers(ctx, w.p, w.m, e.card, e.lane)
	}
	return nil
}

// laneTriggers starts the commands of a lane for a card that entered it and links the last process to the card.
func (a *App) laneTriggers(ctx context.Context, p db.Project, m *manifest.Manifest, card int64, lane string) {
	linked := false
	defer func() {
		if linked {
			a.Bus.Publish("board")
		}
	}()
	for _, t := range m.Triggers {
		if t.Lane != lane {
			continue
		}
		id, err := a.Start(ctx, p.ID, t.Command, manifest.TriggerInput(t, card))
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

// cardFor loads a card the actor may touch, with its project and board manifest.
func cardFor(ctx context.Context, q *db.Queries, act registry.Actor, id int64, w *boardWrite) (db.Card, error) {
	c, err := q.GetCard(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return c, registry.NotFound("deck: no card %d", id)
	}
	if err != nil {
		return c, err
	}
	p, err := q.GetProject(ctx, c.ProjectID)
	if err != nil {
		return c, err
	}
	if p.WorkspaceID != act.Workspace {
		return c, registry.NotFound("deck: no card %d", id)
	}
	if err := inside(act, p.ID); err != nil {
		return c, err
	}
	w.p = p
	w.m, err = boardManifest(ctx, q, p)
	return c, err
}

func liveCard(c db.Card) error {
	if c.DeletedAt.Valid {
		return registry.Invalid("deck: card %d is deleted; restore it first", c.ID)
	}
	return nil
}

func checkRecord(ctx context.Context, q *db.Queries, projectID, id int64) (sql.NullInt64, error) {
	if id == 0 {
		return sql.NullInt64{}, nil
	}
	r, err := q.GetRecord(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && r.ProjectID != projectID {
		return sql.NullInt64{}, registry.NotFound("deck: no record %d in this project", id)
	}
	return sql.NullInt64{Int64: id, Valid: err == nil}, err
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

func (a *App) cardsList(ctx context.Context, act registry.Actor, in struct {
	Project string
	Deleted bool
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	m, err := boardManifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	cards, err := a.Store.Q.ListCards(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	waits, err := waitsOf(ctx, a.Store.Q, p.ID)
	if err != nil {
		return nil, err
	}
	out := boardView{Project: p.Name, Runs: m.Runs(), Lanes: []laneView{}}
	for _, l := range m.Board.Lanes {
		lv := laneView{Name: l.Name, Title: l.Title, Role: l.Role, Cards: []cardView{}}
		for _, c := range cards {
			if c.Lane == l.Name {
				lv.Cards = append(lv.Cards, viewCard(c, waits))
			}
		}
		out.Lanes = append(out.Lanes, lv)
	}
	if in.Deleted {
		deleted, err := a.Store.Q.ListDeletedCards(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out.Deleted = []cardView{}
		for _, c := range deleted {
			out.Deleted = append(out.Deleted, viewCard(c, waits))
		}
	}
	return out, nil
}

func (a *App) cardGet(ctx context.Context, act registry.Actor, in struct{ Card int64 }) (any, error) {
	var v cardView
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := cardFor(ctx, q, act, in.Card, &boardWrite{})
		if err != nil {
			return err
		}
		waits, err := waitsOf(ctx, q, c.ProjectID)
		v = viewCard(c, waits)
		return err
	})
	return v, err
}

// cardResult reads a card back after a write, for the command result.
func cardResult(ctx context.Context, q *db.Queries, id int64) (cardView, error) {
	c, err := q.GetCard(ctx, id)
	if err != nil {
		return cardView{}, err
	}
	waits, err := waitsOf(ctx, q, c.ProjectID)
	return viewCard(c, waits), err
}

func (a *App) cardCreate(ctx context.Context, act registry.Actor, in struct {
	Project, Title, Text, Lane string
	Record                     int64
	Waits                      []int64
}) (any, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, registry.Invalid("deck: a card needs a title")
	}
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	var v cardView
	err = a.writeBoard(ctx, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		w.p = p
		if w.m, err = boardManifest(ctx, q, p); err != nil {
			return err
		}
		lane := in.Lane
		if lane == "" {
			lane = landing(w.m)
		} else if w.m.Lane(lane) == nil {
			return registry.Invalid("deck: project %q has no lane %q", p.Name, lane)
		}
		rec, err := checkRecord(ctx, q, p.ID, in.Record)
		if err != nil {
			return err
		}
		c, err := q.CreateCard(ctx, db.CreateCardParams{ProjectID: p.ID, Title: in.Title, Text: in.Text, Lane: lane, RecordID: rec})
		if err != nil {
			return err
		}
		if err := setWaits(ctx, q, c, in.Waits); err != nil {
			return err
		}
		w.enter(c.ID, lane)
		v, err = cardResult(ctx, q, c.ID)
		return err
	})
	return v, err
}

func (a *App) cardUpdate(ctx context.Context, act registry.Actor, in struct {
	Card   int64
	Title  *string
	Text   *string
	Record *int64
	Waits  *[]int64
}) (any, error) {
	var v cardView
	err := a.writeBoard(ctx, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		c, err := cardFor(ctx, q, act, in.Card, w)
		if err != nil {
			return err
		}
		if err := liveCard(c); err != nil {
			return err
		}
		if in.Title != nil {
			if strings.TrimSpace(*in.Title) == "" {
				return registry.Invalid("deck: a card needs a title")
			}
			c.Title = *in.Title
		}
		if in.Text != nil {
			c.Text = *in.Text
		}
		if in.Record != nil {
			if c.RecordID, err = checkRecord(ctx, q, c.ProjectID, *in.Record); err != nil {
				return err
			}
		}
		if err := q.UpdateCard(ctx, db.UpdateCardParams{Title: c.Title, Text: c.Text, RecordID: c.RecordID, ID: c.ID}); err != nil {
			return err
		}
		if in.Waits != nil {
			if err := setWaits(ctx, q, c, *in.Waits); err != nil {
				return err
			}
		}
		v, err = cardResult(ctx, q, c.ID)
		return err
	})
	return v, err
}

func (a *App) cardMove(ctx context.Context, act registry.Actor, in struct {
	Card   int64
	Lane   string
	Before int64
}) (any, error) {
	var v cardView
	err := a.writeBoard(ctx, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		c, err := cardFor(ctx, q, act, in.Card, w)
		if err != nil {
			return err
		}
		if err := liveCard(c); err != nil {
			return err
		}
		if w.m.Lane(in.Lane) == nil {
			return registry.Invalid("deck: project %q has no lane %q", w.p.Name, in.Lane)
		}
		if err := place(ctx, q, c, in.Lane, in.Before); err != nil {
			return err
		}
		if in.Lane != c.Lane {
			w.enter(c.ID, in.Lane)
		}
		v, err = cardResult(ctx, q, c.ID)
		return err
	})
	return v, err
}

func (a *App) cardDelete(ctx context.Context, act registry.Actor, in struct{ Card int64 }) (any, error) {
	err := a.writeBoard(ctx, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		if _, err := cardFor(ctx, q, act, in.Card, w); err != nil {
			return err
		}
		_, err := q.DeleteCard(ctx, in.Card)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) cardRestore(ctx context.Context, act registry.Actor, in struct{ Card int64 }) (any, error) {
	var v cardView
	err := a.writeBoard(ctx, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		c, err := cardFor(ctx, q, act, in.Card, w)
		if err != nil {
			return err
		}
		if _, err := q.RestoreCard(ctx, c.ID); err != nil {
			return err
		}
		if w.m.Lane(c.Lane) == nil {
			if err := place(ctx, q, c, landing(w.m), 0); err != nil {
				return err
			}
		}
		v, err = cardResult(ctx, q, c.ID)
		return err
	})
	return v, err
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

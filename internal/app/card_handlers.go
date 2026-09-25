package app

import (
	"context"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"strings"
)

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
	err = a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
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
	err := a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
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
	err := a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
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
	err := a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
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
	err := a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
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

package app

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"errors"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type cardsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Deleted bool   `opt:"" desc:"Also list deleted cards that can still be restored"`
}

func (a *App) cardsList(ctx context.Context, act registry.Actor, in cardsListIn) (any, error) {
	p, err := access.Project(ctx, a.Store.Q, act, in.Project)
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

type cardGetIn struct {
	Card int64 `type:"card" desc:"Card id"`
}

func (a *App) cardGet(ctx context.Context, act registry.Actor, in cardGetIn) (any, error) {
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

type cardCreateIn struct {
	Project string  `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Title   string  `desc:"Card title"`
	Text    string  `type:"text" opt:"" desc:"Card text, markdown"`
	Lane    string  `opt:"" desc:"Lane name"`
	Record  int64   `type:"record" opt:"" desc:"Record the card works on"`
	Waits   []int64 `type:"card[]" opt:"" desc:"Cards this one waits for"`
}

func (a *App) cardCreate(ctx context.Context, act registry.Actor, in cardCreateIn) (any, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, registry.Invalid("deck: a card needs a title")
	}
	p, err := access.Project(ctx, a.Store.Q, act, in.Project)
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

type cardUpdateIn struct {
	Card   int64    `type:"card" desc:"Card id"`
	Title  *string  `type:"string" opt:"" desc:"New title"`
	Text   *string  `type:"text" opt:"" desc:"New text, markdown"`
	Record *int64   `type:"record" opt:"" desc:"Record the card works on; 0 unlinks it"`
	Waits  *[]int64 `type:"card[]" opt:"" desc:"All cards this one waits for; an empty list clears them"`
}

func (a *App) cardUpdate(ctx context.Context, act registry.Actor, in cardUpdateIn) (any, error) {
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

type cardMoveIn struct {
	Card   int64  `type:"card" desc:"Card id"`
	Lane   string `desc:"Lane name"`
	Before int64  `type:"card" opt:"" desc:"Place before this card of the lane; the end when omitted"`
}

func (a *App) cardMove(ctx context.Context, act registry.Actor, in cardMoveIn) (any, error) {
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

type cardDeleteIn struct {
	Card int64 `type:"card" desc:"Card id"`
}

func (a *App) cardDelete(ctx context.Context, act registry.Actor, in cardDeleteIn) (any, error) {
	err := a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		if _, err := cardFor(ctx, q, act, in.Card, w); err != nil {
			return err
		}
		_, err := q.DeleteCard(ctx, db.DeleteCardParams{DeletedAt: sql.NullString{String: store.Time(a.Cfg.Now()), Valid: true}, ID: in.Card})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type cardTransferIn struct {
	Card    int64  `type:"card" desc:"Card id"`
	Project string `type:"project" desc:"Project the card moves to, into its inbox lane"`
}

// cardTransfer gives a card to another project: into its inbox lane, with its documents and files, without the
// record, the run and the waits of the old project.
func (a *App) cardTransfer(ctx context.Context, act registry.Actor, in cardTransferIn) (any, error) {
	to, err := access.Project(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var v cardView
	err = a.writeBoard(ctx, act, func(ctx context.Context, q *db.Queries, w *boardWrite) error {
		c, err := cardFor(ctx, q, act, in.Card, w)
		if err != nil {
			return err
		}
		if err := liveCard(c); err != nil {
			return err
		}
		if c.ProjectID == to.ID {
			return registry.Invalid("deck: card %d is already in %s", c.ID, to.Name)
		}
		if c.ProcessID.Valid {
			if p, err := q.GetProcess(ctx, c.ProcessID.Int64); err == nil && !p.FinishedAt.Valid {
				return registry.Conflict("deck: card %d has a run going; stop it or let it finish first", c.ID)
			}
		}
		m, err := boardManifest(ctx, q, to)
		if err != nil {
			return err
		}
		if m.RoleLane("inbox") == nil {
			return registry.Invalid("deck: project %s has no inbox lane for a card", to.Name)
		}
		err = errors.Join(q.TransferCard(ctx, db.TransferCardParams{ProjectID: to.ID, ID: c.ID}), q.DropAllCardEdges(ctx, c.ID),
			q.TransferCardDocuments(ctx, db.TransferCardDocumentsParams{ProjectID: store.ID(to.ID), CardID: store.ID(c.ID)}),
			q.TransferCardFiles(ctx, db.TransferCardFilesParams{ProjectID: to.ID, OwnerID: c.ID}))
		if err != nil {
			return err
		}
		c.ProjectID = to.ID
		w.p, w.m = to, m
		if err := place(ctx, q, c, landing(m), 0); err != nil {
			return err
		}
		w.enter(c.ID, landing(m))
		v, err = cardResult(ctx, q, c.ID)
		return err
	})
	return v, err
}

type cardRestoreIn struct {
	Card int64 `type:"card" desc:"Card id"`
}

func (a *App) cardRestore(ctx context.Context, act registry.Actor, in cardRestoreIn) (any, error) {
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

func (a *App) cardCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("cards_list", "Board",
			"Returns the board of a project: lanes in order with their cards by rank, and recently deleted cards when asked.",
			registry.Everyone, a.cardsList),
		registry.Def("card_get", "Get card",
			"Returns one card with the cards it waits for.",
			registry.Everyone, a.cardGet),
		registry.Def("card_transfer", "Move card to another project",
			"Moves a card into the inbox lane of another project, with its documents and files; its record, run and waits stay behind.",
			registry.OwnerOnly, a.cardTransfer),
		registry.Def("card_create", "Create card",
			"Creates a card at the end of a lane; without a lane it lands in the inbox lane.",
			registry.Everyone, a.cardCreate),
		registry.Def("card_update", "Edit card",
			"Changes the title, text, record or the cards a card waits for; omitted fields stay.",
			registry.Everyone, a.cardUpdate),
		registry.Def("card_move", "Move card",
			"Moves a card to a lane, before another card or to the end; entering a lane starts its commands.",
			registry.Everyone, a.cardMove),
		registry.Def("card_delete", "Delete card",
			"Deletes a card; it can be restored for a day.",
			registry.Everyone, a.cardDelete),
		registry.Def("card_restore", "Restore card",
			"Brings a deleted card back to its lane, or to the inbox lane when that lane is gone.",
			registry.Everyone, a.cardRestore),
	}
}

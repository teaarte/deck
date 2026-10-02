package board

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"errors"
	"time"

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
		Record: c.RecordID.Int64, Process: c.ProcessID.Int64, Waits: append([]int64{}, waits[c.ID]...),
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

// cardFor loads a card the actor may touch, with its project and board manifest.
func cardFor(ctx context.Context, q *db.Queries, act registry.Actor, id int64, w *boardWrite) (db.Card, error) {
	c, err := q.GetCard(ctx, id)
	c, p, err := access.Owned(ctx, q, act, c, err, c.ProjectID, registry.NotFound("deck: no card %d", id))
	if err != nil {
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

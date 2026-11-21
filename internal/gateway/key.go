package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// KeyKept is how long deck keeps the key of a call and what its call gave.
const KeyKept = 24 * time.Hour

// maxKept is the most of what a call gave that deck keeps for a call again with its key.
const maxKept = 64 << 10

// errMadeAgain is a call with the key of one made, which gives what that one gave.
var errMadeAgain = errors.New("made again")

// keyOf is the key a call of c by act with input takes, nil for a call without one or a read.
func keyOf(act registry.Actor, c *registry.Command, input json.RawMessage, key string) *db.TakeCallKeyParams {
	if key == "" || c.Reads() {
		return nil
	}
	return &db.TakeCallKeyParams{WorkspaceID: act.Workspace, Who: RefOf(act).String(), Key: key, Command: c.Name, Input: Canonical(input)}
}

// claim takes the key of a call in the Tx of before, or gives what the call of that key gave; a key of a call still
// being made, of one deck stopped making or of another command refuses the call.
func (g *Gateway) claim(ctx context.Context, q *db.Queries, k db.TakeCallKeyParams) (*registry.Result, error) {
	now := g.st.Now()
	if err := q.ForgetCallKeys(ctx, store.Time(now.Add(-KeyKept))); err != nil {
		return nil, err
	}
	old, err := q.GetCallKey(ctx, db.GetCallKeyParams{WorkspaceID: k.WorkspaceID, Who: k.Who, Key: k.Key})
	switch {
	case errors.Is(err, sql.ErrNoRows) || err == nil && old.State == "rejected":
	case err != nil:
		return nil, err
	case old.Command != k.Command || old.Input != k.Input:
		return nil, registry.Invalid("the key %s is of another call of %s; a new call takes a new key", k.Key, old.Command)
	case old.State == "making":
		return nil, registry.Refuse("%s is being made by this key now; its answer comes once it is done", k.Command)
	case old.State == "lost":
		return nil, registry.Refuse("%s was being made by this key when deck stopped; its outcome is unknown, so it is not made again", k.Command)
	case old.Result == "":
		return nil, registry.Refuse("%s was made by this key already; what it gave is not given twice", k.Command)
	default:
		// A key kept before deck kept the effect holds the data alone, done.
		var r kept
		if json.Unmarshal([]byte(old.Result), &r) != nil || r.Effect == "" {
			return &registry.Result{Effect: registry.Done, Data: json.RawMessage(old.Result)}, nil
		}
		return &registry.Result{Effect: r.Effect, Data: r.Data, Process: r.Process, Ask: r.Ask}, nil
	}
	k.CreatedAt = store.Time(now)
	return nil, q.TakeCallKey(ctx, k)
}

// keepKey writes, in the Tx of after, what the call of a key gave, or that it was refused so the key is taken anew, or
// lost; what a call that gives a secret gave is not kept.
func (g *Gateway) keepKey(ctx context.Context, q *db.Queries, k db.TakeCallKeyParams, c *registry.Command, out any, lost bool, err error) error {
	state, result := "done", ""
	switch {
	case lost:
		state = "lost"
	case err != nil:
		state = "rejected"
	default:
		r := registry.ResultOf(out)
		if data, merr := json.Marshal(r); merr == nil && len(data) <= maxKept && !c.Secret() {
			b, _ := json.Marshal(kept{Effect: r.Effect, Process: r.Process, Ask: r.Ask, Data: data})
			result = string(b)
		}
	}
	return q.SettleCallKey(ctx, db.SettleCallKeyParams{State: state, Result: result, WorkspaceID: k.WorkspaceID, Who: k.Who, Key: k.Key})
}

// kept is what a call of a key gave as deck keeps it: the effect with the data.
type kept struct {
	Effect  registry.Effect `json:"effect"`
	Process int64           `json:"process,omitempty"`
	Ask     int64           `json:"ask,omitempty"`
	Data    json.RawMessage `json:"data"`
}

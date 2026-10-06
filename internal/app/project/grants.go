package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// widens lists what a change by the actor would grant an enabled project; the owner and drafts are not asked.
func (sv *Saver) widens(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, next *manifest.Manifest) ([]string, error) {
	if act.Kind == registry.Owner {
		return nil, nil
	}
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.Status != store.ProjectEnabled {
		return nil, err
	}
	prev, err := docs.Manifest(ctx, q, p)
	if err != nil {
		prev = &manifest.Manifest{}
	}
	return manifest.Widens(prev, next), nil
}

// ask freezes a change that widens rights until the owner allows it; the frozen call applies only while the
// manifest is still at revision.
func (sv *Saver) ask(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, revision int64, widens []string, reason string, call Call) (Saved, error) {
	input := map[string]any{"if_revision": revision}
	for k, v := range call.Input {
		input[k] = v
	}
	raw, _ := json.Marshal(input)
	f := gateway.Freeze(act, call.Command, raw)
	f.As, f.Reason, f.Widens = gateway.AsOwner, reason, widens
	text := fmt.Sprintf("%s wants to change the manifest of %s (%s); it widens: %s", act.Author(), p.Name,
		orText(reason, "no reason given"), strings.Join(widens, "; "))
	row, err := gateway.Hold(ctx, q, act, p.ID, 0, ask.KindGrant, registry.Internal, text, f, time.Time{})
	return Saved{Waiting: row.ID, Widens: widens}, err
}

// AnswerGrant makes the frozen call of an allowed grant as the owner; the grant closes only when it applied.
func (sv *Saver) AnswerGrant(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	switch {
	case answer == "allow":
		out, err := sv.Gateway.Decide(ctx, act, q, answer, nil)
		switch {
		case errors.Is(err, ask.ErrNotOpen):
			return nil, ask.NotOpen
		case registry.Status(err) == 409:
			return nil, registry.Conflict("deck: the manifest changed after this request; deny it and ask again")
		}
		return out, err
	case answer == "deny" || strings.HasPrefix(answer, "deny:"):
		if err := sv.Broker.Answer(ctx, q.ID, answer); err != nil {
			if errors.Is(err, ask.ErrNotOpen) {
				return nil, ask.NotOpen
			}
			return nil, err
		}
		sv.Gateway.Record(ctx, q, "rejected", "the owner said no")
		return map[string]any{"ok": true}, nil
	}
	return nil, registry.Invalid("deck: answer a grant with allow, deny or deny:<reason>")
}

// outdate closes the requests for rights frozen on an older manifest, which no longer apply, and tells the owner.
func outdate(ctx context.Context, q *db.Queries, p db.Project, revision int64) error {
	old, err := q.ExpireGrantAsks(ctx, store.ID(p.ID))
	if err != nil {
		return err
	}
	for _, a := range old {
		text := fmt.Sprintf("A request for rights of %s is outdated: the manifest changed to revision %d; whoever asked asks again if it still needs them. It was: %s",
			p.Name, revision, a.Text)
		if err := q.AddDeckNote(ctx, db.AddDeckNoteParams{ProjectID: p.ID, Text: text}); err != nil {
			return err
		}
	}
	return nil
}

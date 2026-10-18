package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// ask holds a change that widens rights until the owner allows it: the manifest proposed on top of the current
// revision, which the owner's yes merges into the manifest as it is then; a revert applies only to its revision.
func (sv *Saver) ask(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, cur db.Document, revertTo int64, widens []string, reason string, proposed []byte) (Saved, error) {
	command, input := "manifest_save", map[string]any{"project": p.Name, "manifest": string(proposed), "reason": reason, "if_revision": cur.Current}
	if revertTo > 0 {
		own := strings.TrimPrefix(strings.TrimPrefix(reason, docs.RevertReason(revertTo, "")), ": ")
		command, input = "document_revert", map[string]any{"document": cur.ID, "revision": revertTo, "reason": own, "if_revision": cur.Current}
	}
	raw, _ := json.Marshal(input)
	f := gateway.Freeze(act, registry.Builtin+"."+command, raw)
	f.As, f.Reason, f.Widens = gateway.AsOwner, reason, widens
	text := fmt.Sprintf("%s asks to widen the rights of %s: %s", act.Author(), p.Name, strings.Join(widens, "; "))
	row, err := gateway.Hold(ctx, q, act, p.ID, 0, ask.KindGrant, registry.Internal, text, f, time.Time{}, sv.Now())
	return Saved{Waiting: row.ID, Widens: widens}, err
}

// errFrozen marks a grant whose frozen call applies only to the revision it was asked on: a revert.
var errFrozen = errors.New("the manifest changed since it was asked")

// merged is the change a grant holds merged into the current manifest of p, and the revision it merged into; an
// error when the manifest changed the same lines since, or the merge would widen what the grant does not name.
func merged(ctx context.Context, q *db.Queries, p db.Project, a db.Ask) (string, int64, error) {
	var f gateway.Frozen
	var in struct {
		Manifest string `json:"manifest"`
		Base     int64  `json:"if_revision"`
	}
	if err := json.Unmarshal([]byte(a.Payload), &f); err != nil {
		return "", 0, err
	}
	if err := json.Unmarshal(f.Call.Input, &in); err != nil || in.Manifest == "" {
		return "", 0, errFrozen
	}
	d, err := docs.Find(ctx, q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
	if err != nil {
		return "", 0, err
	}
	text := manifest.Canonical(in.Manifest)
	if d.Current != in.Base {
		base, err := docs.Revision(ctx, q, d, in.Base)
		if err != nil {
			return "", 0, err
		}
		cur, err := docs.Revision(ctx, q, d, 0)
		if err != nil {
			return "", 0, err
		}
		var conflicts []docs.Conflict
		if text, conflicts = docs.Merge3(text, raised(base.Text), raised(cur.Text)); len(conflicts) > 0 {
			return "", 0, errors.New("the manifest changed the same lines since")
		}
		if text == raised(cur.Text) {
			return "", 0, errors.New("the manifest has this change already")
		}
	}
	now, err := docs.ManifestAt(ctx, q, p.ID, d.Current)
	if err != nil {
		return "", 0, err
	}
	next, err := manifest.Parse([]byte(text))
	if err != nil {
		return "", 0, fmt.Errorf("merged into the manifest it does not read: %v", err)
	}
	for _, w := range manifest.Widens(now, next) {
		if !slices.Contains(f.Widens, w) {
			return "", 0, fmt.Errorf("merged into the manifest it would also widen: %s", w)
		}
	}
	return text, d.Current, nil
}

// AnswerGrant makes the change of an allowed grant as the owner, merged into the current manifest; the grant closes
// only when it applied.
func (sv *Saver) AnswerGrant(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	switch {
	case answer == "allow":
		p, err := sv.St.Q.GetProject(ctx, q.ProjectID.Int64)
		if err != nil {
			return nil, err
		}
		text, rev, err := merged(ctx, sv.St.Q, p, q)
		switch {
		case errors.Is(err, errFrozen):
		case err != nil:
			return nil, registry.Conflict("deck: %v; deny this request and ask again", err)
		default:
			if q.Payload, err = mergedCall(q.Payload, text, rev); err != nil {
				return nil, err
			}
		}
		out, err := sv.Gateway.Decide(ctx, act, q, answer, nil)
		switch {
		case errors.Is(err, ask.ErrNotOpen):
			return nil, err
		case registry.Status(err) == 409 && rev != 0:
			return nil, registry.Conflict("deck: the manifest changed while you answered; allow it again")
		case registry.Status(err) == 409:
			return nil, registry.Conflict("deck: the manifest changed after this request; deny it and ask again")
		}
		return out, err
	case answer == "deny" || strings.HasPrefix(answer, "deny:"):
		if err := sv.Broker.Answer(ctx, q.ID, answer); err != nil {
			return nil, err
		}
		sv.Gateway.Record(ctx, q, "rejected", "the owner said no")
		return map[string]any{"ok": true}, nil
	}
	return nil, registry.Invalid("deck: answer a grant with allow, deny or deny:<reason>")
}

// mergedCall is the frozen save of a grant with the manifest merged into revision rev.
func mergedCall(payload, text string, rev int64) (string, error) {
	var f gateway.Frozen
	var in map[string]any
	if err := json.Unmarshal([]byte(payload), &f); err != nil {
		return "", err
	}
	if err := json.Unmarshal(f.Call.Input, &in); err != nil {
		return "", err
	}
	in["manifest"], in["if_revision"] = text, rev
	raw, err := json.Marshal(in)
	f.Call.Input = raw
	return f.String(), err
}

// outdate closes the requests for rights whose change no longer merges into the manifest at revision, and tells the
// owner; it gives them for the journal after the commit.
func outdate(ctx context.Context, q *db.Queries, p db.Project, revision int64, now time.Time) ([]db.Ask, error) {
	open, err := q.ListOpenGrantAsks(ctx, store.ID(p.ID))
	if err != nil {
		return nil, err
	}
	var old []db.Ask
	for _, a := range open {
		_, _, why := merged(ctx, q, p, a)
		if why == nil {
			continue
		}
		if n, err := q.ExpireAsk(ctx, a.ID); err != nil || n == 0 {
			if err != nil {
				return nil, err
			}
			continue
		}
		text := fmt.Sprintf("A request for rights of %s is outdated at revision %d of the manifest: %v; whoever asked asks again if it still needs them. It was: %s",
			p.Name, revision, why, a.Text)
		if err := q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(now), ProjectID: p.ID, Text: text}); err != nil {
			return nil, err
		}
		old = append(old, a)
	}
	return old, nil
}

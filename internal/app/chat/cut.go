package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

// cutView is what a reply that broke off left: the calls it made, what of them the owner can undo, and the
// providers it can go again on, for this reply only.
type cutView struct {
	Did   []string   `json:"did"`
	Undo  []undoView `json:"undo"`
	Retry []action   `json:"retry"`
}

// undoView is a change a broken reply left, with the button that takes it back.
type undoView struct {
	Text   string `json:"text"`
	Action action `json:"action"`
}

// broke reports whether a reply ended without its answer: an error or an interruption, not a stop of the owner.
func broke(p db.Process) bool {
	return p.State == process.StateError || p.State == process.StateInterrupted
}

// did are the calls a process made that change something, in order, as short lines.
func (ch *Service) did(ctx context.Context, processID int64) []string {
	calls, err := ch.St.Q.ListToolCalls(ctx, store.ID(processID))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, c := range calls {
		if registry.Reads(c.Tool) != nil || c.Outcome == "rejected" {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %s", c.Tool, c.Outcome, untrusted.Line(c.Params, 160)))
	}
	return out
}

// cut is what a broken reply left and how it can go again; nil for one that did not break.
func (ch *Service) cut(ctx context.Context, p db.Process, message int64) *cutView {
	if !broke(p) {
		return nil
	}
	v := &cutView{Did: ch.did(ctx, p.ID), Undo: []undoView{}, Retry: []action{}}
	left, _ := process.LeftBy(ctx, ch.St.Q, p.ID)
	for _, l := range left {
		var a action
		switch {
		case l.Kind == process.LeftChange && l.Undo:
			a = chatAction("undo", registry.Builtin+".feed_undo", map[string]any{"entry": l.ID})
		case l.Kind == process.LeftRecord:
			a = chatAction("delete", registry.Builtin+".record_delete", map[string]any{"record": l.ID})
			a["confirm"], a["parts"] = true, manifest.RecordParts
		case l.Kind == process.LeftFile:
			a = chatAction("delete", registry.Builtin+".file_delete", map[string]any{"file": l.ID})
			a["confirm"] = true
		default:
			continue
		}
		v.Undo = append(v.Undo, undoView{Text: l.Text, Action: a})
	}
	for _, name := range ch.others(ctx, p) {
		a := chatAction("retry", registry.Builtin+".chat_retry", map[string]any{"message": message, "provider": name})
		a["label"] = name
		v.Retry = append(v.Retry, a)
	}
	return v
}

// others are the providers the master's reply may go again on, but the one it broke on.
func (ch *Service) others(ctx context.Context, p db.Process) []string {
	cmd, err := ch.replyCommand(ctx, p.ProjectID)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(ch.Fits(ctx, p.ProjectID, cmd), func(n string) bool { return n == p.Provider })
}

// replyCommand is the command of the master that answers the owner.
func (ch *Service) replyCommand(ctx context.Context, masterID int64) (*manifest.Command, error) {
	p, err := ch.St.Q.GetProject(ctx, masterID)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, ch.St.Q, p)
	if err != nil {
		return nil, err
	}
	if cmd := m.Command(m.EventCommand(manifest.OnMessage)); cmd != nil {
		return cmd, nil
	}
	return nil, registry.Invalid("deck: project master has no trigger on %s", manifest.OnMessage)
}

// again is a reply that goes once more on another provider, with what the broken one did.
type again struct {
	provider, note string
}

type chatRetryIn struct {
	Message  int64  `desc:"Id of the owner's message whose reply broke off"`
	Provider string `desc:"Provider to answer on, for this reply only"`
}

// chatRetry answers the messages of a broken reply again on another provider; the topic keeps its own after it.
func (ch *Service) chatRetry(ctx context.Context, act registry.Actor, in chatRetryIn) (any, error) {
	m, err := ch.St.Q.GetMessage(ctx, in.Message)
	if errors.Is(err, sql.ErrNoRows) || err == nil && m.ProcessID == 0 {
		return nil, registry.NotFound("deck: no reply of message %d", in.Message)
	}
	if err != nil {
		return nil, err
	}
	t, err := ch.ownTopic(ctx, ch.St.Q, act, m.TopicID)
	if err != nil {
		return nil, registry.NotFound("deck: no message %d", in.Message)
	}
	p, err := ch.St.Q.GetProcess(ctx, m.ProcessID)
	if err != nil {
		return nil, err
	}
	if mp, err := access.Master(ctx, ch.St.Q, t.WorkspaceID); err != nil || mp.ID != p.ProjectID || !broke(p) {
		return nil, registry.Conflict("deck: the reply of message %d did not break off", in.Message)
	}
	if !slices.Contains(ch.others(ctx, p), in.Provider) {
		return nil, registry.Invalid("deck: %s is not another provider the master may answer on now", in.Provider)
	}
	note := master.Text("broken", struct {
		Was, Error string
		Did        []string
	}{p.Provider, untrusted.Line(p.Error, 300), ch.did(ctx, p.ID)})
	ch.start.Lock()
	err = ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.ReleaseMessages(ctx, db.ReleaseMessagesParams{TopicID: t.ID, ProcessID: p.ID})
		if err == nil && n == 0 {
			err = registry.Conflict("deck: the messages of this reply went to another one already")
		}
		return err
	})
	if err == nil {
		ch.mu.Lock()
		if ch.again == nil {
			ch.again = map[int64]again{}
		}
		ch.again[t.ID] = again{provider: in.Provider, note: strings.TrimSpace(note)}
		ch.mu.Unlock()
	}
	ch.start.Unlock()
	if err != nil {
		return nil, err
	}
	ch.Go(func() { ch.pump(context.WithoutCancel(ctx), t.ID) })
	return map[string]any{"topic": t.ID, "provider": in.Provider}, nil
}

// againOf takes the provider and note a topic's next reply goes with, once.
func (ch *Service) againOf(topic int64) again {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	a := ch.again[topic]
	delete(ch.again, topic)
	return a
}

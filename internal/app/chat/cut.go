package chat

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// cutView is what a broken or stopped reply left: why, its steps and last words, its calls, what of them the owner can
// undo, and the providers it can go again on for this reply only; a stopped one goes on on its own.
type cutView struct {
	Stopped bool       `json:"stopped,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	Steps   int        `json:"steps"`
	Said    string     `json:"said,omitempty"`
	Did     []string   `json:"did"`
	Undo    []undoView `json:"undo"`
	Retry   []action   `json:"retry"`
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

// stopped reports whether the owner stopped a reply.
func stopped(p db.Process) bool {
	return p.State == process.StateStopped && p.ErrorType == fault.Stopped
}

// cut is what a broken or stopped reply left and how it can go again; nil for one that ended by itself.
func (ch *Service) cut(ctx context.Context, p db.Process, message int64, tr func(string) string) *cutView {
	if !broke(p) && !stopped(p) {
		return nil
	}
	tools, _ := ch.Runner.Tail(p.ID, workLines, "tool")
	v := &cutView{Stopped: stopped(p), Steps: len(tools), Said: ch.Runner.Said(p.ID), Did: process.Did(ctx, ch.St.Q, p.ID),
		Retry: []action{}}
	why := p.ErrorType
	if p.State == process.StateInterrupted {
		why = cmp.Or(why, fault.DeckRestart)
	}
	if why = fault.Said(why); why != "" && !v.Stopped {
		v.Reason = tr(why)
	}
	v.Undo = ch.undos(ctx, p.ID)
	if v.Stopped {
		v.Retry = append(v.Retry, chatAction("continue", registry.Builtin+".chat_retry", map[string]any{"message": message, "provider": p.Provider}))
		return v
	}
	for _, name := range ch.others(ctx, p) {
		a := chatAction("retry", registry.Builtin+".chat_retry", map[string]any{"message": message, "provider": name})
		a["label"] = name
		v.Retry = append(v.Retry, a)
	}
	return v
}

// undos are what a process left that the owner can take back, each with its button.
func (ch *Service) undos(ctx context.Context, processID int64) []undoView {
	out := []undoView{}
	left, err := process.LeftBy(ctx, ch.St.Q, processID)
	if err != nil {
		slog.Error("what a process left", "process", processID, "err", err)
	}
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
		out = append(out, undoView{Text: l.Text, Action: a})
	}
	return out
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

// again is a reply that goes once more on another provider, told what the broken one it follows did.
type again struct {
	provider, note string
	follows        int64
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
	if mp, err := access.Master(ctx, ch.St.Q, t.WorkspaceID); err != nil || mp.ID != p.ProjectID || !broke(p) && !stopped(p) {
		return nil, registry.Conflict("deck: the reply of message %d did not break off and was not stopped", in.Message)
	}
	switch {
	case stopped(p) && in.Provider != p.Provider:
		return nil, registry.Invalid("deck: a stopped reply goes on on its own provider %s", p.Provider)
	case broke(p) && !slices.Contains(ch.others(ctx, p), in.Provider):
		return nil, registry.Invalid("deck: %s is not another provider the master may answer on now", in.Provider)
	}
	note := ch.Runner.HandOnNote(ctx, p, p.Provider, untrusted.Line(p.Error, 300))
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
		ch.again[t.ID] = again{provider: in.Provider, note: strings.TrimSpace(note), follows: p.ID}
		ch.mu.Unlock()
	}
	ch.start.Unlock()
	if err != nil {
		return nil, err
	}
	ch.Go(func(context.Context) { ch.pump(context.WithoutCancel(ctx), t.ID) })
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

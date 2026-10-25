package chat

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// roleDeck is a message deck writes itself: how a work started from the topic ended or what it waits for.
const roleDeck = "deck"

// toldState is what a message of deck keeps in its context: the state of the work it tells of.
type toldState struct {
	State string `json:"state"`
}

// workTopic is the topic a process other than a reply was started from, or 0.
func (ch *Service) workTopic(ctx context.Context, p db.Process) (int64, int64) {
	var in struct{ Topic int64 }
	if json.Unmarshal([]byte(p.Input), &in) != nil || in.Topic == 0 || ch.replyOf(ctx, p) != 0 {
		return 0, 0
	}
	proj, err := ch.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return 0, 0
	}
	t, err := ch.St.Q.GetTopic(ctx, in.Topic)
	if err != nil || t.WorkspaceID != proj.WorkspaceID {
		return 0, 0
	}
	return t.ID, proj.WorkspaceID
}

// tell writes once into the topic a work was started from how it ended or what it waits for; the notes the work
// wrote itself are read then, so the owner is not called twice for one thing.
func (ch *Service) tell(ctx context.Context, p db.Process) {
	topic, ws := ch.workTopic(ctx, p)
	if topic == 0 {
		return
	}
	text, key := untrusted.Cut(p.Result, 4000), fmt.Sprintf("deck:%d:end", p.ID)
	switch {
	case !p.FinishedAt.Valid && p.State != process.StateWaitingAnswer:
		return
	case p.State == process.StateError || p.State == process.StateInterrupted:
		text = p.Error
	case !p.FinishedAt.Valid:
		asks, _ := ch.St.Q.ListOpenAsksOfProcess(ctx, p.ID)
		text, key = "", fmt.Sprintf("deck:%d:wait:%s", p.ID, cmp.Or(p.ReopenedAt.String, p.StartedAt))
		if i := slices.IndexFunc(asks, func(a db.Ask) bool { return a.Kind == ask.KindQuestion || a.Kind == ask.KindConfirm }); i >= 0 {
			text, key = untrusted.Cut(asks[i].Text, 500), fmt.Sprintf("deck:%d:ask:%d", p.ID, asks[i].ID)
		}
	}
	state, _ := json.Marshal(toldState{State: p.State})
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.FindMessageByKey(ctx, db.FindMessageByKeyParams{WorkspaceID: ws, RetryKey: key}); !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(ch.Now()), TopicID: topic, Role: roleDeck,
			Text: strings.TrimSpace(text), Context: string(state), ProcessID: p.ID, RetryKey: key}); err != nil {
			return err
		}
		if err := q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(ch.Now()), ID: topic}); err != nil {
			return err
		}
		if !p.FinishedAt.Valid {
			return nil
		}
		return q.ReadNotesOfProcess(ctx, store.ID(p.ID))
	})
	if err != nil {
		slog.Error("tell the topic of a work", "process", p.ID, "err", err)
	}
}

// toldView is how a work started from the topic ended or what it waits for, in the owner's words.
type toldView struct {
	procView
	// Reason is why a failed work failed; Error is what deck wrote of it.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// told is the view of a message of deck: the state it was written at, the work it tells of and why that failed.
func (ch *Service) told(ctx context.Context, m db.ChatMessage, ws map[string]string, tr func(string) string) *toldView {
	p, err := ch.St.Q.GetProcess(ctx, m.ProcessID)
	if err != nil {
		return nil
	}
	var s toldState
	_ = json.Unmarshal([]byte(m.Context), &s)
	at := p
	at.State = cmp.Or(s.State, p.State)
	question := ""
	if !slices.Contains(process.Final, at.State) {
		at.FinishedAt, question = sql.NullString{}, m.Text
	} else {
		at.FinishedAt = sql.NullString{String: cmp.Or(p.FinishedAt.String, m.CreatedAt), Valid: true}
	}
	proj, _ := ch.St.Q.GetProject(ctx, p.ProjectID)
	v := &toldView{procView: procView{ID: p.ID, Project: proj.Name, Command: p.Command, State: at.State, Open: openProcess(p.ID),
		Shown: process.Show(at, question, false, tr)}}
	if at.State == process.StateError || at.State == process.StateInterrupted {
		why := p.ErrorType
		if at.State == process.StateInterrupted {
			why = cmp.Or(why, fault.DeckRestart)
		}
		if why = fault.Said(why); why != "" {
			v.Reason = tr(why)
		}
		v.Error = m.Text
		if !(p.Kind == process.KindScript && slices.Contains(fault.ByScript, p.ErrorType)) {
			v.Error = manifest.TrText(ws, m.Text)
		}
	}
	return v
}

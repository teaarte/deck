package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	roleOwner  = "owner"
	roleMaster = "master"
	// retellTail is how many messages after the summary a new session gets word for word.
	retellTail = 30
	// summaryDue is how many messages since the summary make deck ask the master to update it.
	summaryDue = 20
)

// chatQueue runs the replies of topics one at a time per topic; a pump that is asked while it runs, even from
// a hook it set off, leaves the topic for the running loop.
type chatQueue struct {
	busy bool
	due  map[int64]bool
}

// summaryName is the document of project master with the summary of a topic.
func summaryName(topic int64) string { return fmt.Sprintf("topics/%d/summary.md", topic) }

// pump starts the reply to the owner's messages of a topic that no reply took yet.
func (a *App) pump(ctx context.Context, topic int64) {
	a.chatMu.Lock()
	if a.chat.due == nil {
		a.chat.due = map[int64]bool{}
	}
	a.chat.due[topic] = true
	if a.chat.busy {
		a.chatMu.Unlock()
		return
	}
	a.chat.busy = true
	for {
		next := int64(0)
		for t := range a.chat.due {
			next = t
			break
		}
		if next == 0 {
			a.chat.busy = false
			a.chatMu.Unlock()
			return
		}
		delete(a.chat.due, next)
		a.chatMu.Unlock()
		if err := a.reply(ctx, next); err != nil && ctx.Err() == nil {
			slog.Error("chat reply", "topic", next, "err", err)
		}
		a.chatMu.Lock()
	}
}

// PumpChats starts the replies to messages that a stop of deck left without one.
func (a *App) PumpChats(ctx context.Context) {
	ids, err := a.Store.Q.ListTopicsWithPending(ctx)
	if err != nil {
		slog.Error("chat topics with messages", "err", err)
		return
	}
	for _, id := range ids {
		a.pump(ctx, id)
	}
}

// reply hands the waiting messages of a topic to the master: into its parked reply, or a new reply process
// that resumes the topic's session of its provider or starts one with a retelling.
func (a *App) reply(ctx context.Context, topicID int64) error {
	a.chatStart.Lock()
	defer a.chatStart.Unlock()
	t, err := a.Store.Q.GetTopic(ctx, topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	pending, err := a.Store.Q.ListPendingMessages(ctx, topicID)
	if err != nil || len(pending) == 0 {
		return err
	}
	first, last := pending[0].ID, pending[len(pending)-1].ID
	open, err := a.Store.Q.OpenTopicProcess(ctx, topicID)
	if err == nil {
		if q := a.openQuestion(ctx, open.ID); q != 0 {
			texts := make([]string, 0, len(pending))
			for _, m := range pending {
				texts = append(texts, strings.TrimSpace(m.Text))
			}
			if err := a.Asks.Answer(ctx, q, strings.Join(texts, "\n\n")); err == nil {
				return a.takeMessages(ctx, t, open.ID, open.SessionID, "", last)
			}
		}
		if open.State == process.StateRunning {
			news, _, err := a.chatPrompt(ctx, t, pending, "", "", false)
			if err != nil || a.Runner.Say(ctx, open.ID, "deck: the owner wrote while you work.\n"+news) == process.Refused {
				return err
			}
			return a.takeMessages(ctx, t, open.ID, open.SessionID, "", last)
		}
		if open.State != process.StateWaitingAnswer {
			return nil
		}
		msg, sign, err := a.chatPrompt(ctx, t, pending, open.SessionID, open.Provider, true)
		if err != nil {
			return err
		}
		if err := a.Runner.Remark(ctx, open.ID, msg); err != nil {
			return err
		}
		return a.takeMessages(ctx, t, open.ID, open.SessionID, sign, last)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	p, err := a.masterProject(ctx, t.WorkspaceID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return err
	}
	reply := m.EventCommand(manifest.OnMessage)
	cmd := m.Command(reply)
	if cmd == nil {
		return registry.Invalid("deck: project master has no trigger on %s", manifest.OnMessage)
	}
	pv, err := a.For(ctx, p.ID, cmd)
	if err != nil {
		return err
	}
	o := process.Origin{Session: uuid.NewString()}
	sign := ""
	s, err := a.Store.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: topicID, Provider: pv.Name()})
	switch {
	case err == nil:
		o.Session, o.Resume = s.SessionID, true
		if o.Message, sign, err = a.chatPrompt(ctx, t, pending, s.SessionID, pv.Name(), true); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		// A new session is told everything before the first waiting message, and the memory of now.
		if _, sign, err = a.masterMemory(ctx, p, topicID); err != nil {
			return err
		}
		err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetChatSession(ctx, db.SetChatSessionParams{TopicID: topicID, Provider: pv.Name(), SessionID: o.Session,
				Seen: first - 1, Memory: sign})
		})
		if err != nil {
			return err
		}
	default:
		return err
	}
	retell, err := a.retelling(ctx, t, first)
	if err != nil {
		return err
	}
	news, _, err := a.chatPrompt(ctx, t, pending, "", "", false)
	if err != nil {
		return err
	}
	o.Note = strings.TrimSpace(retell + "\n\n" + news)
	id, err := a.Runner.Start(ctx, p.ID, m, reply, map[string]any{"topic": topicID}, o)
	if err != nil {
		return err
	}
	return a.takeMessages(ctx, t, id, o.Session, sign, last)
}

// takeMessages gives the waiting messages up to through to the reply process, and keeps the sign of the memory
// its session was given, when it was given one.
func (a *App) takeMessages(ctx context.Context, t db.ChatTopic, processID int64, session, sign string, through int64) error {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.TakeMessages(ctx, db.TakeMessagesParams{ProcessID: processID, TopicID: t.ID, Through: through}); err != nil || sign == "" {
			return err
		}
		return q.SetChatMemory(ctx, db.SetChatMemoryParams{Memory: sign, TopicID: t.ID, SessionID: session})
	})
	a.Bus.Publish("chat")
	return err
}

// chatPrompt is what a reply turn gets: on resume the messages its session missed, then the owner's new
// messages with their screens, the card of the topic and where its summary is.
func (a *App) chatPrompt(ctx context.Context, t db.ChatTopic, pending []db.ChatMessage, session, provider string, resume bool) (string, string, error) {
	type message struct{ Where, Text string }
	type card struct {
		ID             int64
		Project, Title string
	}
	v := struct {
		MemoryChanged          bool
		Memory, Retold, Missed string
		Topic                  int64
		Pending                []message
		Card                   *card
		Summarized             bool
		Summary                string
		Grown                  int64
	}{Topic: t.ID, Summary: summaryName(t.ID)}
	sign := ""
	if resume {
		s, err := a.Store.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: t.ID, Provider: provider})
		if err == nil && s.SessionID == session {
			mp, err := a.masterProject(ctx, t.WorkspaceID)
			if err != nil {
				return "", "", err
			}
			mem, now, err := a.masterMemory(ctx, mp, t.ID)
			if err != nil {
				return "", "", err
			}
			sign = now
			v.MemoryChanged, v.Memory = now != s.Memory, mem
			missed, err := a.Store.Q.ListMessages(ctx, db.ListMessagesParams{TopicID: t.ID, After: s.Seen})
			if err != nil {
				return "", "", err
			}
			var lines []db.ChatMessage
			for _, m := range missed {
				if m.ID < pending[0].ID {
					lines = append(lines, m)
				}
			}
			switch {
			case len(lines) > retellTail:
				if v.Retold, err = a.retelling(ctx, t, pending[0].ID); err != nil {
					return "", "", err
				}
			case len(lines) > 0:
				v.Missed = untrusted.Block("Messages new to you, oldest first", transcript(lines))
			}
		}
	}
	for _, m := range pending {
		v.Pending = append(v.Pending, message{Where: screenText(m.Context), Text: strings.TrimSpace(m.Text)})
	}
	if t.CardID.Valid {
		if c, err := a.Store.Q.GetCard(ctx, t.CardID.Int64); err == nil {
			p, _ := a.Store.Q.GetProject(ctx, c.ProjectID)
			v.Card = &card{ID: c.ID, Project: p.Name, Title: untrusted.Line(c.Title, 200)}
		}
	}
	grown, err := a.sinceSummary(ctx, t)
	if err != nil {
		return "", "", err
	}
	since, _, err := a.summary(ctx, t)
	if err != nil {
		return "", "", err
	}
	v.Summarized = since != ""
	if grown >= summaryDue {
		v.Grown = grown
	}
	return prose("turn", v), sign, nil
}

// sinceSummary counts the messages of a topic after the current revision of its summary.
func (a *App) sinceSummary(ctx context.Context, t db.ChatTopic) (int64, error) {
	since, _, err := a.summary(ctx, t)
	if err != nil {
		return 0, err
	}
	return a.Store.Q.CountMessagesAfter(ctx, db.CountMessagesAfterParams{TopicID: t.ID, Since: since})
}

// summary is the current summary of a topic and when it was written; empty when the master wrote none.
func (a *App) summary(ctx context.Context, t db.ChatTopic) (since, text string, err error) {
	p, err := a.masterProject(ctx, t.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	d, err := a.Store.Q.FindDocument(ctx, docs.Ref{Scope: docs.ProjectScope(p), Name: summaryName(t.ID)}.Find())
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	r, err := docs.Revision(ctx, a.Store.Q, d, 0)
	return r.CreatedAt, r.Text, err
}

// retelling is the start of a topic for a session that did not see it: the summary and the messages after it,
// at most retellTail, before the message before.
func (a *App) retelling(ctx context.Context, t db.ChatTopic, before int64) (string, error) {
	since, text, err := a.summary(ctx, t)
	if err != nil {
		return "", err
	}
	tail, err := a.Store.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: before, Lim: retellTail})
	if err != nil {
		return "", err
	}
	var after []db.ChatMessage
	for _, m := range tail {
		if m.CreatedAt >= since {
			after = append(after, m)
		}
	}
	if strings.TrimSpace(text) == "" && len(after) == 0 {
		return "", nil
	}
	v := struct{ Summary, After string }{}
	if strings.TrimSpace(text) != "" {
		v.Summary = untrusted.Block("Summary of the topic", text)
	}
	if len(after) > 0 {
		v.After = untrusted.Block("Messages after the summary, word for word, oldest first", transcript(after))
	}
	return prose("retelling", v), nil
}

func transcript(ms []db.ChatMessage) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, strings.TrimSpace(m.Text))
	}
	return b.String()
}

// screenContext is the screen of the owner a message was written on.
type screenContext struct {
	Project string `json:"project,omitempty"`
	Screen  string `json:"screen,omitempty"`
	Record  int64  `json:"record,omitempty"`
	Card    int64  `json:"card,omitempty"`
	Process int64  `json:"process,omitempty"`
}

func screenText(raw string) string {
	var s screenContext
	if raw == "" || json.Unmarshal([]byte(raw), &s) != nil {
		return ""
	}
	var parts []string
	if s.Project != "" {
		parts = append(parts, "project "+untrusted.Line(s.Project, 60))
	}
	if s.Screen != "" {
		parts = append(parts, "screen "+untrusted.Line(s.Screen, 60))
	}
	for _, f := range []struct {
		name string
		id   int64
	}{{"record", s.Record}, {"card", s.Card}, {"process", s.Process}} {
		if f.id != 0 {
			parts = append(parts, fmt.Sprintf("%s %d", f.name, f.id))
		}
	}
	return strings.Join(parts, ", ")
}

// replyOf is the topic of a reply process of the built-in master, or 0 for any other process.
func (a *App) replyOf(ctx context.Context, p db.Process) int64 {
	proj, err := a.Store.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Builtin == 0 {
		return 0
	}
	if m, err := docs.Manifest(ctx, a.Store.Q, proj); err != nil || p.Command != m.EventCommand(manifest.OnMessage) {
		return 0
	}
	var in struct{ Topic int64 }
	_ = json.Unmarshal([]byte(p.Input), &in)
	return in.Topic
}

// Turn keeps the final text of a reply turn as the master's message in its topic.
func (h chatHook) Turn(ctx context.Context, p db.Process, text string) {
	a := h.a
	topic := a.replyOf(ctx, p)
	if topic == 0 || strings.TrimSpace(text) == "" {
		return
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := q.AddMessage(ctx, db.AddMessageParams{TopicID: topic, Role: roleMaster, Text: strings.TrimSpace(text), ProcessID: p.ID})
		if err != nil {
			return err
		}
		if err := q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(a.Cfg.Now()), ID: topic}); err != nil {
			return err
		}
		return q.SeeChat(ctx, db.SeeChatParams{Seen: m.ID, TopicID: topic, SessionID: p.SessionID})
	})
	if err != nil {
		slog.Error("chat reply", "process", p.ID, "err", err)
	}
	a.Bus.Publish("chat")
}

// chatHook keeps the master's replies in their topics and goes on with the messages a topic got while its
// reply ran, once the reply closes or parks.
type chatHook struct{ a *App }

func (h chatHook) Closed(ctx context.Context, p db.Process) { h.goOn(ctx, p) }
func (h chatHook) Parked(ctx context.Context, p db.Process) { h.goOn(ctx, p) }

func (h chatHook) goOn(ctx context.Context, p db.Process) {
	if topic := h.a.replyOf(ctx, p); topic != 0 {
		h.a.Bus.Publish("chat")
		h.a.pump(context.WithoutCancel(ctx), topic)
	}
}

// openQuestion is a question of the owner that the process waits on; a message of the topic answers it.
func (a *App) openQuestion(ctx context.Context, processID int64) int64 {
	asks, err := a.Store.Q.ListOpenAsksOfProcess(ctx, processID)
	if err != nil {
		return 0
	}
	for _, q := range asks {
		if q.Kind == ask.KindQuestion && q.State == "open" {
			return q.ID
		}
	}
	return 0
}

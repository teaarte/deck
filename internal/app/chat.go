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

	"deck2/internal/app/docs"
	"deck2/internal/ask"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

const (
	roleOwner  = "owner"
	roleMaster = "master"
	// retellTail is how many messages after the summary a new session gets word for word.
	retellTail = 30
	// summaryDue is how many messages since the summary make deck ask the master to update it.
	summaryDue   = 20
	replyCommand = "reply"
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
			if err != nil || !a.Runner.Say(ctx, open.ID, "deck: the owner wrote while you work.\n"+news) {
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
		ok, err := a.Runner.Tell(ctx, open.ID, msg)
		if err != nil || !ok {
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
	cmd := m.Command(replyCommand)
	if cmd == nil {
		return registry.Invalid("deck: project master has no command %s", replyCommand)
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
	id, err := a.Runner.Start(ctx, p.ID, m, replyCommand, map[string]any{"topic": topicID}, o)
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
	var b strings.Builder
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
			if sign = now; now != s.Memory {
				b.WriteString("deck: the memory changed since this session last got it; here it is now.\n" + mem + "\n")
			}
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
				retell, err := a.retelling(ctx, t, pending[0].ID)
				if err != nil {
					return "", "", err
				}
				b.WriteString("deck: much happened in this topic since your last turn.\n" + retell + "\n")
			case len(lines) > 0:
				b.WriteString("deck: these messages of the topic came after your last turn; another provider may have answered them.\n")
				b.WriteString(untrusted.Block("Messages new to you, oldest first", transcript(lines)) + "\n\n")
			}
		}
	}
	if len(pending) > 1 {
		fmt.Fprintf(&b, "The owner's %d new messages in chat topic %d, oldest first:\n", len(pending), t.ID)
	} else {
		fmt.Fprintf(&b, "The owner's new message in chat topic %d:\n", t.ID)
	}
	for _, m := range pending {
		if where := screenText(m.Context); where != "" {
			b.WriteString("(written on " + where + ")\n")
		}
		b.WriteString(strings.TrimSpace(m.Text) + "\n\n")
	}
	if t.CardID != 0 {
		if c, err := a.Store.Q.GetCard(ctx, t.CardID); err == nil {
			p, _ := a.Store.Q.GetProject(ctx, c.ProjectID)
			fmt.Fprintf(&b, "This topic is about card %d of project %s, titled %s.\n", c.ID, p.Name, untrusted.Line(c.Title, 200))
		}
	}
	grown, err := a.sinceSummary(ctx, t)
	if err != nil {
		return "", "", err
	}
	fmt.Fprintf(&b, "The summary of this topic is document %s of project %s.", summaryName(t.ID), master.Name)
	if grown >= summaryDue {
		fmt.Fprintf(&b, " The topic has %d messages after its summary: update it by the topic-summary skill.", grown)
	}
	return b.String(), sign, nil
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
	var b strings.Builder
	b.WriteString("deck: this session did not see the earlier part of this topic; here it is.\n")
	if strings.TrimSpace(text) != "" {
		b.WriteString(untrusted.Block("Summary of the topic", text) + "\n")
	}
	if len(after) > 0 {
		b.WriteString(untrusted.Block("Messages after the summary, word for word, oldest first", transcript(after)) + "\n")
	}
	return b.String(), nil
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
func (a *App) replyOf(ctx context.Context, processID int64) (db.Process, int64) {
	p, err := a.Store.Q.GetProcess(ctx, processID)
	if err != nil || p.Command != replyCommand {
		return p, 0
	}
	proj, err := a.Store.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Builtin == 0 {
		return p, 0
	}
	var in struct{ Topic int64 }
	_ = json.Unmarshal([]byte(p.Input), &in)
	return p, in.Topic
}

// chatTurn keeps the final text of a reply turn as the master's message in its topic.
func (a *App) chatTurn(ctx context.Context, processID int64, text string) {
	p, topic := a.replyOf(ctx, processID)
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
		slog.Error("chat reply", "process", processID, "err", err)
	}
	a.Bus.Publish("chat")
}

// chatClosed goes on with the messages a topic got while its reply ran; it runs when the reply closes or parks.
func (a *App) chatClosed(ctx context.Context, processID int64) {
	if _, topic := a.replyOf(ctx, processID); topic != 0 {
		a.Bus.Publish("chat")
		a.pump(context.WithoutCancel(ctx), topic)
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

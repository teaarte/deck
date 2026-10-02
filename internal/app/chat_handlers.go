package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// maxChatText is the longest message the owner sends in one go.
const maxChatText = 20000

type topicView struct {
	ID        int64        `json:"id"`
	Title     string       `json:"title"`
	Card      int64        `json:"card,omitempty"`
	Archived  bool         `json:"archived"`
	UpdatedAt string       `json:"updated_at"`
	Last      *messageView `json:"last,omitempty"`
	// Reply is the reply process of the topic still going, with its state.
	Reply *replyView `json:"reply,omitempty"`
}

type replyView struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error,omitempty"`
}

type messageView struct {
	ID        int64           `json:"id"`
	Role      string          `json:"role"`
	Text      string          `json:"text"`
	Screen    json.RawMessage `json:"screen,omitempty"`
	Process   int64           `json:"process,omitempty"`
	Reply     *replyView      `json:"reply,omitempty"`
	CreatedAt string          `json:"created_at"`
}

func viewMessage(m db.ChatMessage) messageView {
	v := messageView{ID: m.ID, Role: m.Role, Text: m.Text, Process: m.ProcessID, CreatedAt: m.CreatedAt}
	if m.Context != "" {
		v.Screen = json.RawMessage(m.Context)
	}
	return v
}

func viewReply(p db.Process) *replyView {
	return &replyView{Process: p.ID, State: p.State, Provider: p.Provider, Error: p.Error}
}

// ownTopic is a topic of the actor's workspace.
func (a *App) ownTopic(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.ChatTopic, error) {
	t, err := q.GetTopic(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && t.WorkspaceID != act.Workspace {
		return t, registry.NotFound("deck: no chat topic %d", id)
	}
	return t, err
}

// topicTitle is the first line of the first message, cut short.
func topicTitle(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > 60 {
		line = string([]rune(line)[:60]) + "…"
	}
	return line
}

type chatSendIn struct {
	Text     string `type:"text" desc:"The message"`
	Topic    int64  `opt:"" desc:"Chat topic; a new topic when omitted"`
	Screen   string `type:"text" opt:"" desc:"The screen the owner is on, as JSON: project, screen, record, card, process"`
	Card     int64  `type:"card" opt:"" desc:"Card a new topic is about"`
	RetryKey string `json:"retry_key" opt:"" desc:"A key of this message: sending it again with the same key returns the first one"`
}

func (a *App) chatSend(ctx context.Context, act registry.Actor, in chatSendIn) (any, error) {
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return nil, registry.Invalid("deck: the message is empty")
	case len(text) > maxChatText:
		return nil, registry.Invalid("deck: a message is at most %d bytes", maxChatText)
	case in.Topic != 0 && in.Card != 0:
		return nil, registry.Invalid("deck: a card goes with a new topic only")
	}
	screen := ""
	if strings.TrimSpace(in.Screen) != "" {
		var s screenContext
		if err := json.Unmarshal([]byte(in.Screen), &s); err != nil {
			return nil, registry.Invalid("deck: screen is a JSON object: %v", err)
		}
		b, _ := json.Marshal(s)
		screen = string(b)
	}
	if _, err := a.masterProject(ctx, act.Workspace); err != nil {
		return nil, err
	}
	var msg db.ChatMessage
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if in.RetryKey != "" {
			old, err := q.FindMessageByKey(ctx, db.FindMessageByKeyParams{WorkspaceID: act.Workspace, RetryKey: in.RetryKey})
			if err == nil {
				msg = old
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var t db.ChatTopic
		var err error
		if in.Topic != 0 {
			if t, err = a.ownTopic(ctx, q, act, in.Topic); err != nil {
				return err
			}
		} else {
			if in.Card != 0 {
				c, err := q.GetCard(ctx, in.Card)
				if err != nil || c.DeletedAt.Valid {
					return registry.NotFound("deck: no card %d", in.Card)
				}
				if _, err := projectOf(ctx, q, act, c.ProjectID, registry.NotFound("deck: no card %d", in.Card)); err != nil {
					return err
				}
			}
			if t, err = q.CreateTopic(ctx, db.CreateTopicParams{WorkspaceID: act.Workspace, Title: topicTitle(text), CardID: in.Card}); err != nil {
				return err
			}
		}
		if msg, err = q.AddMessage(ctx, db.AddMessageParams{TopicID: t.ID, Role: roleOwner, Text: text, Context: screen, RetryKey: in.RetryKey}); err != nil {
			return err
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(a.Cfg.Now()), ID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("chat")
	a.pump(context.WithoutCancel(ctx), msg.TopicID)
	out := map[string]any{"topic": msg.TopicID, "message": msg.ID}
	if m, err := a.Store.Q.GetMessage(ctx, msg.ID); err == nil && m.ProcessID != 0 {
		out["process"] = m.ProcessID
	}
	return out, nil
}

type chatTopicsIn struct {
	Archived bool  `opt:"" desc:"The archived topics instead"`
	Limit    int64 `opt:"" desc:"At most this many"`
}

func (a *App) chatTopics(ctx context.Context, act registry.Actor, in chatTopicsIn) (any, error) {
	in.Limit = registry.Limit(in.Limit, 100, 500)
	ts, err := a.Store.Q.ListTopics(ctx, db.ListTopicsParams{WorkspaceID: act.Workspace, Archived: b2i(in.Archived), Lim: in.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]topicView, 0, len(ts))
	for _, t := range ts {
		v := topicView{ID: t.ID, Title: t.Title, Card: t.CardID, Archived: t.Archived == 1, UpdatedAt: t.UpdatedAt}
		if last, err := a.Store.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 1}); err == nil && len(last) == 1 {
			m := viewMessage(last[0])
			m.Text = untrusted.Cut(m.Text, 200)
			v.Last = &m
		}
		if p, err := a.Store.Q.OpenTopicProcess(ctx, t.ID); err == nil {
			v.Reply = viewReply(p)
		}
		out = append(out, v)
	}
	return out, nil
}

type chatMessagesIn struct {
	Topic int64 `desc:"Chat topic"`
	After int64 `opt:"" desc:"Only messages after this one"`
	Limit int64 `opt:"" desc:"At most this many of the newest, 200 by default"`
}

func (a *App) chatMessages(ctx context.Context, act registry.Actor, in chatMessagesIn) (any, error) {
	t, err := a.ownTopic(ctx, a.Store.Q, act, in.Topic)
	if err != nil {
		return nil, err
	}
	in.Limit = registry.Limit(in.Limit, 200, 1000)
	rows, err := a.Store.Q.ListMessages(ctx, db.ListMessagesParams{TopicID: t.ID, After: in.After})
	if err != nil {
		return nil, err
	}
	if int64(len(rows)) > in.Limit {
		rows = rows[int64(len(rows))-in.Limit:]
	}
	replies := map[int64]*replyView{}
	ms := make([]messageView, 0, len(rows))
	for _, r := range rows {
		m := viewMessage(r)
		if r.Role == roleOwner && r.ProcessID != 0 {
			if _, seen := replies[r.ProcessID]; !seen {
				replies[r.ProcessID] = nil
				if p, err := a.Store.Q.GetProcess(ctx, r.ProcessID); err == nil {
					replies[r.ProcessID] = viewReply(p)
				}
			}
			m.Reply = replies[r.ProcessID]
		}
		ms = append(ms, m)
	}
	v := topicView{ID: t.ID, Title: t.Title, Card: t.CardID, Archived: t.Archived == 1, UpdatedAt: t.UpdatedAt}
	if p, err := a.Store.Q.OpenTopicProcess(ctx, t.ID); err == nil {
		v.Reply = viewReply(p)
	}
	return map[string]any{"topic": v, "messages": ms}, nil
}

type chatTopicArchiveIn struct {
	Topic    int64 `desc:"Chat topic"`
	Archived bool  `desc:"Archive when true, bring back when false"`
}

func (a *App) chatTopicArchive(ctx context.Context, act registry.Actor, in chatTopicArchiveIn) (any, error) {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := a.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		_, err := q.ArchiveTopic(ctx, db.ArchiveTopicParams{Archived: b2i(in.Archived), ID: in.Topic})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("chat")
	return map[string]any{"topic": in.Topic, "archived": in.Archived}, nil
}

type chatTopicDeleteIn struct {
	Topic int64 `desc:"Chat topic"`
}

func (a *App) chatTopicDelete(ctx context.Context, act registry.Actor, in chatTopicDeleteIn) (any, error) {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := a.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		_, err := q.DeleteTopic(ctx, db.DeleteTopicParams{DeletedAt: sql.NullString{String: store.Time(a.Cfg.Now()), Valid: true}, ID: in.Topic})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.chatStart.Lock()
	p, err := a.Store.Q.OpenTopicProcess(ctx, in.Topic)
	a.chatStart.Unlock()
	if err == nil {
		_ = a.Runner.Stop(ctx, p.ID, "deck: the owner deleted the chat topic")
	}
	a.Bus.Publish("chat")
	return map[string]any{"topic": in.Topic, "deleted": true}, nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (a *App) chatCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("chat_send", "Tell the master",
			"Sends the owner's message to the master in a chat topic, a new one when none is given; the master's reply comes into the same topic.",
			registry.OwnerOnly, a.chatSend),
		registry.Def("chat_topics", "Chat topics",
			"Lists the owner's chat topics with the master, newest first, with their last message and the reply still going.",
			registry.OwnerOnly, a.chatTopics),
		registry.Def("chat_messages", "Chat messages",
			"Returns the messages of a chat topic, oldest first, with the state of the reply each one got.",
			registry.OwnerOnly, a.chatMessages),
		registry.Def("chat_topic_archive", "Archive topic",
			"Archives a chat topic or brings it back; a new message brings it back too.",
			registry.OwnerOnly, a.chatTopicArchive),
		registry.Def("chat_topic_delete", "Delete topic",
			"Deletes a chat topic with its messages and stops its reply if one is going.",
			registry.OwnerOnly, a.chatTopicDelete),
	}
}

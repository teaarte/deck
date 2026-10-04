package chat

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/master"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// maxChatText is the longest message the owner sends in one go.
const maxChatText = 20000

// ownTopic is a topic of the actor's workspace.
func (ch *Service) ownTopic(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.ChatTopic, error) {
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
	Text     string   `type:"text" desc:"The message"`
	Topic    int64    `opt:"" desc:"Chat topic; a new topic when omitted"`
	Screen   string   `type:"json" opt:"" desc:"The screen the owner is on, as JSON: project, screen, record, card, process"`
	Card     int64    `type:"card" opt:"" desc:"Card a new topic is about"`
	RetryKey string   `json:"retry_key" opt:"" desc:"A key of this message: sending it again with the same key returns the first one"`
	Files    []string `type:"file[]" opt:"" desc:"Uploads of project master the owner attaches; the turn that takes the message gets copies"`
}

type chatInterviewIn struct {
	Card   int64  `type:"card" desc:"The card the master interviews the owner about"`
	Screen string `type:"json" opt:"" desc:"The screen the owner is on, as JSON: project, screen, record, card, process"`
}

// chatInterview opens a topic about a card in which the master interviews the owner and writes the spec into it.
func (ch *Service) chatInterview(ctx context.Context, act registry.Actor, in chatInterviewIn) (any, error) {
	c, err := ch.St.Q.GetCard(ctx, in.Card)
	if _, _, err = access.Owned(ctx, ch.St.Q, act, c, err, c.ProjectID, registry.NotFound("deck: no card %d", in.Card)); err != nil {
		return nil, err
	}
	text := master.Text("interview", struct {
		Card  int64
		Title string
	}{c.ID, untrusted.Line(c.Title, 200)})
	return ch.chatSend(ctx, act, chatSendIn{Text: text, Card: c.ID, Screen: in.Screen})
}

func (ch *Service) chatSend(ctx context.Context, act registry.Actor, in chatSendIn) (any, error) {
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return nil, registry.Invalid("deck: the message is empty")
	case len(text) > maxChatText:
		return nil, registry.Invalid("deck: a message is at most %d bytes", maxChatText)
	case in.Topic != 0 && in.Card != 0:
		return nil, registry.Invalid("deck: a card goes with a new topic only")
	}
	var s screenContext
	if strings.TrimSpace(in.Screen) != "" {
		if err := json.Unmarshal([]byte(in.Screen), &s); err != nil {
			return nil, registry.Invalid("deck: screen is a JSON object: %v", err)
		}
	}
	if in.RetryKey != "" {
		if old, err := ch.St.Q.FindMessageByKey(ctx, db.FindMessageByKeyParams{WorkspaceID: act.Workspace, RetryKey: in.RetryKey}); err == nil {
			return ch.sent(ctx, old), nil
		}
	}
	s.Files = nil
	for _, f := range in.Files {
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, registry.Invalid("deck: a file is the id of an upload: %q", f)
		}
		s.Files = append(s.Files, id)
	}
	screen := ""
	if b, _ := json.Marshal(s); string(b) != "{}" {
		screen = string(b)
	}
	master, err := access.Master(ctx, ch.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	var msg db.ChatMessage
	err = ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
			if t, err = ch.ownTopic(ctx, q, act, in.Topic); err != nil {
				return err
			}
		} else {
			if in.Card != 0 {
				c, err := q.GetCard(ctx, in.Card)
				if err != nil || c.DeletedAt.Valid {
					return registry.NotFound("deck: no card %d", in.Card)
				}
				if _, err := access.Of(ctx, q, act, c.ProjectID, registry.NotFound("deck: no card %d", in.Card)); err != nil {
					return err
				}
			}
			if t, err = q.CreateTopic(ctx, db.CreateTopicParams{WorkspaceID: act.Workspace, Title: topicTitle(text), CardID: store.ID(in.Card)}); err != nil {
				return err
			}
		}
		if msg, err = q.AddMessage(ctx, db.AddMessageParams{TopicID: t.ID, Role: roleOwner, Text: text, Context: screen, RetryKey: in.RetryKey}); err != nil {
			return err
		}
		for _, id := range s.Files {
			n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: FileOwner, OwnerID: msg.ID, ID: id, ProjectID: master.ID, FromKind: "upload", FromID: 0})
			if err != nil {
				return err
			}
			if n == 0 {
				return registry.NotFound("deck: no upload %d of project master", id)
			}
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(ch.Now()), ID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	return ch.sent(ctx, msg), nil
}

// sent wakes the master for a message of the owner and says where it went.
func (ch *Service) sent(ctx context.Context, msg db.ChatMessage) map[string]any {
	ch.pump(context.WithoutCancel(ctx), msg.TopicID)
	out := map[string]any{"topic": msg.TopicID, "message": msg.ID}
	if m, err := ch.St.Q.GetMessage(ctx, msg.ID); err == nil && m.ProcessID != 0 {
		out["process"] = m.ProcessID
	}
	return out
}

type chatTopicsIn struct {
	Archived bool  `opt:"" desc:"The archived topics instead"`
	Limit    int64 `opt:"" desc:"At most this many"`
}

func (ch *Service) chatTopics(ctx context.Context, act registry.Actor, in chatTopicsIn) ([]topicView, error) {
	return ch.topics(ctx, act.Workspace, in.Archived, 0, registry.Limit(in.Limit, 100, 500))
}

type chatMessagesIn struct {
	Topic int64 `desc:"Chat topic"`
	After int64 `opt:"" desc:"Only messages after this one"`
	Limit int64 `opt:"" desc:"At most this many of the newest, 200 by default"`
}

// topicMessages is a topic with its messages, oldest first.
type topicMessages struct {
	Topic    topicView     `json:"topic"`
	Messages []messageView `json:"messages"`
}

func (ch *Service) chatMessages(ctx context.Context, act registry.Actor, in chatMessagesIn) (topicMessages, error) {
	t, err := ch.ownTopic(ctx, ch.St.Q, act, in.Topic)
	if err != nil {
		return topicMessages{}, err
	}
	in.Limit = registry.Limit(in.Limit, 200, 1000)
	rows, err := ch.St.Q.ListMessages(ctx, db.ListMessagesParams{TopicID: t.ID, After: in.After})
	if err != nil {
		return topicMessages{}, err
	}
	if int64(len(rows)) > in.Limit {
		rows = rows[int64(len(rows))-in.Limit:]
	}
	ts, err := ch.topics(ctx, act.Workspace, false, t.ID, 1)
	if err != nil || len(ts) == 0 {
		return topicMessages{}, err
	}
	return topicMessages{Topic: ts[0], Messages: ch.messages(ctx, t.ID, rows)}, nil
}

type chatTopicArchiveIn struct {
	Topic    int64 `desc:"Chat topic"`
	Archived bool  `desc:"Archive when true, bring back when false"`
}

func (ch *Service) chatTopicArchive(ctx context.Context, act registry.Actor, in chatTopicArchiveIn) (any, error) {
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := ch.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		_, err := q.ArchiveTopic(ctx, db.ArchiveTopicParams{Archived: b2i(in.Archived), ID: in.Topic})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"topic": in.Topic, "archived": in.Archived}, nil
}

type chatTopicDeleteIn struct {
	Topic int64 `desc:"Chat topic"`
}

func (ch *Service) chatTopicDelete(ctx context.Context, act registry.Actor, in chatTopicDeleteIn) (any, error) {
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := ch.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		_, err := q.DeleteTopic(ctx, db.DeleteTopicParams{DeletedAt: sql.NullString{String: store.Time(ch.Now()), Valid: true}, ID: in.Topic})
		return err
	})
	if err != nil {
		return nil, err
	}
	ch.start.Lock()
	p, err := ch.St.Q.OpenTopicProcess(ctx, in.Topic)
	ch.start.Unlock()
	if err == nil {
		_ = ch.Runner.Stop(ctx, p.ID, "deck: the owner deleted the chat topic")
	}
	return map[string]any{"topic": in.Topic, "deleted": true}, nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Commands are the commands of the chat.
func (ch *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("chat_send", "Tell the master",
			"Sends the owner's message to the master in a chat topic, a new one when none is given; the master's reply comes into the same topic.",
			registry.OwnerOnly, ch.chatSend).Changes("chat"),
		registry.Def("chat_interview", "Interview about a card",
			"Opens a chat topic about a card in which the master interviews the owner and writes the spec into the card.",
			registry.OwnerOnly, ch.chatInterview).Changes("chat"),
		registry.Read("chat_topics", "Chat topics",
			"Lists the owner's chat topics with the master, newest first, with their last message and the reply still going.",
			registry.OwnerOnly, ch.chatTopics).On("chat", "processes"),
		registry.Read("chat_messages", "Chat messages",
			"Returns the messages of a chat topic, oldest first, with the state of the reply each one got.",
			registry.OwnerOnly, ch.chatMessages).On("chat", "processes"),
		registry.Read("chat", "Chat",
			"Gives the chat with the master as a chat block shows it: the topics, and of the topic picked its messages, the questions of its reply and the lines of the turn going on.",
			registry.OwnerOnly, ch.chat).On("chat", "asks", "log", "processes"),
		registry.Def("chat_topic_archive", "Archive topic",
			"Archives a chat topic or brings it back; a new message brings it back too.",
			registry.OwnerOnly, ch.chatTopicArchive).Changes("chat"),
		registry.Def("chat_topic_delete", "Delete topic",
			"Deletes a chat topic with its messages and stops its reply if one is going.",
			registry.OwnerOnly, ch.chatTopicDelete).Changes("chat"),
	}
}

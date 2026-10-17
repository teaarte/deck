// Package chat is the chat of the owner with the master: topics, messages, the reply turns of the master and the
// memory its agents get.
package chat

import (
	"cmp"
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	roleOwner  = "owner"
	roleMaster = "master"
	// FileOwner is the owner kind of a file attached to a message.
	FileOwner = "chat_message"
	// retellTail is how many messages after the summary a new session gets word for word.
	retellTail = 30
	// summaryDue is how many messages since the summary make deck ask the master to update it.
	summaryDue = 20
)

// Service keeps the chat. For is the provider a command runs on; Runner starts and goes on with the replies.
type Service struct {
	St     *store.Store
	Bus    *events.Bus
	Now    func() time.Time
	Asks   *ask.Broker
	Runner *process.Runner
	For    func(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Named is a provider by name; Fits the providers a command may start on now.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	Fits  func(ctx context.Context, projectID int64, cmd *manifest.Command) []string
	// Able are the providers that have what a command requires, the models a topic may keep.
	Able func(ctx context.Context, workspace int64, cmd *manifest.Command) []string
	// DrainFiles removes the files whose rows are gone.
	DrainFiles func(ctx context.Context)
	// Go runs work beside the calls unless deck stops; the replies it leaves go at the next start.
	Go func(func()) bool
	// Words are deck's phrases in a language.
	Words func(lang string) map[string]string

	mu    sync.Mutex
	queue chatQueue
	// unstarted is why the reply of a topic did not start, until one does; again, the provider and note its next
	// reply goes with after one broke off.
	unstarted map[int64]string
	again     map[int64]again
	// start keeps a reply starting apart from a topic being deleted.
	start sync.Mutex
}

// NewHook is the hook of the runner that keeps the master's replies in their topics.
func (ch *Service) NewHook() Hook { return Hook{ch: ch} }

// summaryName is the document of project master with the summary of a topic.
func summaryName(topic int64) string { return fmt.Sprintf("topics/%d/summary.md", topic) }

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
	Screen   string   `type:"json" opt:"" desc:"The screen the owner is on, as JSON: project, screen, record, process"`
	RetryKey string   `json:"retry_key" opt:"" desc:"A key of this message: sending it again with the same key returns the first one"`
	Files    []string `type:"file[]" opt:"" desc:"Uploads of project master the owner attaches; the turn that takes the message gets copies"`
	Model    string   `opt:"" desc:"The model the replies of the topic go on from now; the master's model when omitted on a new topic"`
}

func (ch *Service) chatSend(ctx context.Context, act registry.Actor, in chatSendIn) (any, error) {
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return nil, registry.Invalid("deck: the message is empty")
	case len(text) > maxChatText:
		return nil, registry.Invalid("deck: a message is at most %d bytes", maxChatText)
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
	if in.Model != "" {
		if err := ch.topicModel(ctx, master, in.Model); err != nil {
			return nil, err
		}
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
		} else if t, err = q.CreateTopic(ctx, db.CreateTopicParams{WorkspaceID: act.Workspace, Title: topicTitle(text)}); err != nil {
			return err
		}
		if in.Model != "" && in.Model != t.Model {
			if err := q.SetTopicModel(ctx, db.SetTopicModelParams{Model: in.Model, ID: t.ID}); err != nil {
				return err
			}
		}
		if msg, err = q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(ch.Now()), TopicID: t.ID, Role: roleOwner, Text: text, Context: screen, RetryKey: in.RetryKey}); err != nil {
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

// topicModel refuses a model the master's replies cannot run on.
func (ch *Service) topicModel(ctx context.Context, master db.Project, model string) error {
	cmd, err := ch.replyCommand(ctx, master.ID)
	if err != nil {
		return err
	}
	if able := ch.Able(ctx, master.WorkspaceID, cmd); !slices.Contains(able, model) {
		return registry.Invalid("deck: the master's replies do not run on %q; one of: %s", model, strings.Join(able, ", "))
	}
	return nil
}

type chatTopicModelIn struct {
	Topic int64  `desc:"Chat topic"`
	Model string `opt:"" desc:"The model its replies go on from the next one; the master's model when empty"`
}

// chatTopicModel sets the model a topic keeps; a reply going on keeps its own.
func (ch *Service) chatTopicModel(ctx context.Context, act registry.Actor, in chatTopicModelIn) (any, error) {
	master, err := access.Master(ctx, ch.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Model != "" {
		if err := ch.topicModel(ctx, master, in.Model); err != nil {
			return nil, err
		}
	}
	err = ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := ch.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		return q.SetTopicModel(ctx, db.SetTopicModelParams{Model: in.Model, ID: in.Topic})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"topic": in.Topic, "model": in.Model}, nil
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
		_, err := q.ArchiveTopic(ctx, db.ArchiveTopicParams{Archived: store.Bool(in.Archived), ID: in.Topic})
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
		_ = ch.Runner.Cancel(ctx, p.ID, "deck: the owner deleted the chat topic", act.Kind == registry.Owner)
	}
	return map[string]any{"topic": in.Topic, "deleted": true}, nil
}

type chatMessageDeleteIn struct {
	Message int64 `desc:"Id of the owner's message the master has not taken yet"`
}

// chatMessageDelete deletes a message of the owner with its files while no turn of the master has it.
func (ch *Service) chatMessageDelete(ctx context.Context, act registry.Actor, in chatMessageDeleteIn) (any, error) {
	ch.start.Lock()
	defer ch.start.Unlock()
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := q.GetMessage(ctx, in.Message)
		if errors.Is(err, sql.ErrNoRows) {
			return registry.NotFound("deck: no message %d", in.Message)
		}
		if err != nil {
			return err
		}
		if _, err := ch.ownTopic(ctx, q, act, m.TopicID); err != nil {
			return registry.NotFound("deck: no message %d", in.Message)
		}
		switch {
		case m.Role != roleOwner:
			return registry.Refuse("deck: only a message of the owner can be deleted")
		case m.ProcessID != 0:
			return registry.Conflict("deck: the master's turn #%d has taken this message; it stays", m.ProcessID)
		}
		if n, err := q.DeleteWaitingMessage(ctx, m.ID); err != nil || n == 0 {
			return cmp.Or(err, registry.Conflict("deck: the master has taken this message; it stays"))
		}
		return q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: FileOwner, OwnerID: m.ID})
	})
	if err != nil {
		return nil, err
	}
	ch.DrainFiles(ctx)
	return map[string]any{"ok": true}, nil
}

// Commands are the commands of the chat.
func (ch *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("chat_send", "Tell the master",
			"Sends the owner's message to the master in a chat topic, a new one when none is given; the master's reply comes into the same topic.",
			registry.OwnerOnly, ch.chatSend),
		registry.Read("chat", "Chat",
			"Gives the chat with the master as a chat block shows it: the topics, and of the topic picked its messages, the questions of its reply and the lines of the turn going on.",
			registry.OwnerOnly, ch.chat).On("chat", "asks", "log", "processes", "feed", "collection", "health", "models", "assignments"),
		registry.Def("chat_topic_archive", "Archive topic",
			"Archives a chat topic or brings it back; a new message brings it back too.",
			registry.OwnerOnly, ch.chatTopicArchive),
		registry.Def("chat_message_delete", "Delete message",
			"Deletes a message of the owner with its files while the master has not taken it; a message a turn took stays.",
			registry.OwnerOnly, ch.chatMessageDelete),
		registry.Def("chat_retry", "Answer again on another provider",
			"Answers the messages of a reply that broke off once more, on another provider the master may use, for this reply only.",
			registry.OwnerOnly, ch.chatRetry),
		registry.Def("chat_topic_model", "Pick the model of a topic",
			"Sets the model the master's replies in a chat topic go on from the next one; empty brings back the master's own model.",
			registry.OwnerOnly, ch.chatTopicModel),
		registry.Def("chat_topic_delete", "Delete topic",
			"Deletes a chat topic with its messages and stops its reply if one is going.",
			registry.OwnerOnly, ch.chatTopicDelete),
	}
}

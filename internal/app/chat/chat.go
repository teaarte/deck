// Package chat is the chat of the owner with the master: topics, messages, the reply turns of the master and the
// memory its agents get.
package chat

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
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
	St *store.Store
	// Emit tells deck's observers of a change no write of the store tells.
	Emit   func(events.Event)
	Now    func() time.Time
	Asks   *ask.Broker
	Runner *process.Runner
	For    func(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Named is a provider by name; Fits the providers a command may start on now.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	Fits  func(ctx context.Context, projectID int64, cmd *manifest.Command) []string
	// Able are the providers that have what a command requires, the models a topic may keep.
	Able func(ctx context.Context, workspace int64, cmd *manifest.Command) []string
	// Facts say each model in short in a language, the words naming it for its mark and the group of its provider.
	Facts func(ctx context.Context, workspace int64, lang string) func(model string) (note, logo, group string)
	// Go runs work beside the calls unless deck stops; the replies it leaves go at the next start.
	Go func(func(context.Context)) bool
	// Language is the language of the interface the workspace picked: auto, en or ru.
	Language func(ctx context.Context, workspace int64) string
	// Lookup is a command by name.
	Lookup func(ctx context.Context, workspace int64, name string) (*registry.Command, error)
	// Decisions are what processes wait for the owner to decide but their questions, each with its buttons.
	Decisions func(ctx context.Context, act registry.Actor, processes []int64, lang string) (any, error)

	mu    sync.Mutex
	queue chatQueue
	// unstarted is why the reply of a topic did not start, until one does; again, the provider and note its next
	// reply goes with after one broke off.
	unstarted map[int64]string
	again     map[int64]again
	// start keeps a reply starting apart from a topic or message being deleted, and the notes of deck a reply is told
	// from the reply of another topic.
	start sync.Mutex
}

// Hook keeps the master's replies in their topics.
func (s *Service) Hook() process.Hook { return replies{s: s} }

// summaryName is the document of project master with the summary of a topic.
func summaryName(topic int64) string { return fmt.Sprintf("topics/%d/summary.md", topic) }

// maxChatText is the longest message the owner sends in one go.
const maxChatText = 20000

// ownTopic is a topic of the actor's workspace.
func (s *Service) ownTopic(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.ChatTopic, error) {
	t, err := q.GetTopic(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && t.WorkspaceID != act.Workspace {
		return t, registry.NotFound("no chat topic %d", id)
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

func (s *Service) chatSend(ctx context.Context, act registry.Actor, in chatSendIn) (any, error) {
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return nil, registry.Invalid("the message is empty")
	case len(text) > maxChatText:
		return nil, registry.Invalid("a message is at most %d bytes", maxChatText)
	}
	var it screenContext
	if strings.TrimSpace(in.Screen) != "" {
		if err := json.Unmarshal([]byte(in.Screen), &it); err != nil {
			return nil, registry.Invalid("screen is a JSON object: %v", err)
		}
	}
	if in.RetryKey != "" {
		if old, err := s.St.Q.FindMessageByKey(ctx, db.FindMessageByKeyParams{WorkspaceID: act.Workspace, RetryKey: in.RetryKey}); err == nil {
			return s.sent(ctx, old), nil
		}
	}
	it.Files = nil
	for _, f := range in.Files {
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, registry.Invalid("a file is the id of an upload: %q", f)
		}
		it.Files = append(it.Files, id)
	}
	screen := ""
	if b, _ := json.Marshal(it); string(b) != "{}" {
		screen = string(b)
	}
	master, err := access.Master(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Model != "" {
		if err := s.topicModel(ctx, master, in.Model); err != nil {
			return nil, err
		}
	}
	var msg db.ChatMessage
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
			if t, err = s.ownTopic(ctx, q, act, in.Topic); err != nil {
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
		if msg, err = q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(s.Now()), TopicID: t.ID, Role: roleOwner, Text: text, Context: screen, RetryKey: in.RetryKey}); err != nil {
			return err
		}
		// The owner answered in words: the options and forms of the topic wait for them no more.
		if err := q.DismissTopicQuestions(ctx, t.ID); err != nil {
			return err
		}
		for _, id := range it.Files {
			n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: FileOwner, OwnerID: msg.ID, ID: id, ProjectID: master.ID, FromKind: "upload", FromID: 0})
			if err != nil {
				return err
			}
			if n == 0 {
				return registry.NotFound("no upload %d of project master", id)
			}
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(s.Now()), ID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	return s.sent(ctx, msg), nil
}

// sent wakes the master for a message of the owner and says where it went.
func (s *Service) sent(ctx context.Context, msg db.ChatMessage) map[string]any {
	s.pump(context.WithoutCancel(ctx), msg.TopicID)
	out := map[string]any{"topic": msg.TopicID, "message": msg.ID}
	if m, err := s.St.Q.GetMessage(ctx, msg.ID); err == nil && m.ProcessID != 0 {
		out["process"] = m.ProcessID
	}
	return out
}

// topicModel refuses a model the master's replies cannot run on.
func (s *Service) topicModel(ctx context.Context, master db.Project, model string) error {
	cmd, err := s.replyCommand(ctx, master.ID)
	if err != nil {
		return err
	}
	if able := s.Able(ctx, master.WorkspaceID, cmd); !slices.Contains(able, model) {
		return registry.Invalid("the master's replies do not run on %q; one of: %s", model, strings.Join(able, ", "))
	}
	return nil
}

type chatTopicModelSetIn struct {
	Topic int64  `desc:"Chat topic"`
	Model string `opt:"" desc:"The model its replies go on from the next one; the master's model when empty"`
}

// chatTopicModel sets the model a topic keeps; a reply going on keeps its own.
func (s *Service) chatTopicModel(ctx context.Context, act registry.Actor, in chatTopicModelSetIn) (any, error) {
	master, err := access.Master(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Model != "" {
		if err := s.topicModel(ctx, master, in.Model); err != nil {
			return nil, err
		}
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := s.ownTopic(ctx, q, act, in.Topic); err != nil {
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

func (s *Service) chatTopicArchive(ctx context.Context, act registry.Actor, in chatTopicArchiveIn) (any, error) {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := s.ownTopic(ctx, q, act, in.Topic); err != nil {
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

func (s *Service) chatTopicDelete(ctx context.Context, act registry.Actor, in chatTopicDeleteIn) (any, error) {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := s.ownTopic(ctx, q, act, in.Topic); err != nil {
			return err
		}
		if err := q.DismissTopicQuestions(ctx, in.Topic); err != nil {
			return err
		}
		_, err := q.DeleteTopic(ctx, db.DeleteTopicParams{DeletedAt: sql.NullString{String: store.Time(s.Now()), Valid: true}, ID: in.Topic})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.start.Lock()
	p, err := s.St.Q.OpenTopicProcess(ctx, in.Topic)
	s.start.Unlock()
	if err == nil {
		_ = s.Runner.Cancel(ctx, p.ID, "deck: the owner deleted the chat topic", act.Kind == registry.Owner)
	}
	return map[string]any{"topic": in.Topic, "deleted": true}, nil
}

type chatMessageDeleteIn struct {
	Message int64 `desc:"Id of the owner's message the master has not taken yet"`
}

// chatMessageDelete deletes a message of the owner with its files while no turn of the master has it.
func (s *Service) chatMessageDelete(ctx context.Context, act registry.Actor, in chatMessageDeleteIn) (any, error) {
	s.start.Lock()
	defer s.start.Unlock()
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := q.GetMessage(ctx, in.Message)
		if errors.Is(err, sql.ErrNoRows) {
			return registry.NotFound("no message %d", in.Message)
		}
		if err != nil {
			return err
		}
		if _, err := s.ownTopic(ctx, q, act, m.TopicID); err != nil {
			return registry.NotFound("no message %d", in.Message)
		}
		switch {
		case m.Role != roleOwner:
			return registry.Refuse("only a message of the owner can be deleted")
		case m.ProcessID != 0:
			return registry.Conflict("the master's turn #%d has taken this message; it stays", m.ProcessID)
		}
		if n, err := q.DeleteWaitingMessage(ctx, m.ID); err != nil || n == 0 {
			return cmp.Or(err, registry.Conflict("the master has taken this message; it stays"))
		}
		return q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: FileOwner, OwnerID: m.ID})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// Commands are the commands of the chat.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.chatCommands(), s.viewsCommands(), s.cutCommands())
}

func (s *Service) chatCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("chat_send", "Tell the master",
			"Sends the owner's message to the master in a chat topic, a new one when none is given; the master's reply comes into the same topic.",
			registry.OwnerOnly, s.chatSend),
		registry.Def("chat_topic_archive", "Archive topic",
			"Archives a chat topic or brings it back; a new message brings it back too.",
			registry.OwnerOnly, s.chatTopicArchive),
		registry.Def("chat_message_delete", "Delete message",
			"Deletes a message of the owner with its files while the master has not taken it; a message a turn took stays.",
			registry.OwnerOnly, s.chatMessageDelete),
		registry.Def("chat_topic_model_set", "Pick the model of a topic",
			"Sets the model the master's replies in a chat topic go on from the next one; empty brings back the master's own model.",
			registry.OwnerOnly, s.chatTopicModel),
		registry.Def("chat_block_show", "Show a block in the chat",
			"Shows the owner one block in the topic this reply answers: options to pick, a form to fill (a number with minimum and maximum slides, its examples the marks), a table, a chart or a card, its data in place; the final text of the turn goes over it. Options and a form are a question that waits for the owner in the topic and among what waits for them; the answer, given in either, comes as the next message of the topic.",
			registry.OwnerSide, s.chatBlockShow),
		registry.Def("chat_topic_delete", "Delete topic",
			"Deletes a chat topic with its messages and stops its reply if one is going.",
			registry.OwnerOnly, s.chatTopicDelete),
	}
}

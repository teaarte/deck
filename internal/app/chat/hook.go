package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// replyOf is the topic of a reply process of the built-in master, or 0 for any other process.
func (s *Service) replyOf(ctx context.Context, p db.Process) int64 {
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Builtin == 0 {
		return 0
	}
	if m, err := docs.Manifest(ctx, s.St.Q, proj); err != nil || p.Command != m.EventCommand(manifest.OnMessage) {
		return 0
	}
	topic, _ := topicOf(p)
	return topic
}

// topicOf is the chat topic the input of a process names, 0 for none.
func topicOf(p db.Process) (int64, error) {
	var in struct {
		Topic int64 `json:"topic"`
	}
	err := json.Unmarshal([]byte(p.Input), &in)
	return in.Topic, err
}

// Turn keeps the final text of a reply turn as the master's message in its topic, over the block it showed last.
func (h replies) Turn(ctx context.Context, p db.Process, text string) {
	s := h.s
	topic := s.replyOf(ctx, p)
	if topic == 0 || strings.TrimSpace(text) == "" {
		return
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		block, err := q.FillShownText(ctx, db.FillShownTextParams{Text: strings.TrimSpace(text), TopicID: topic, ProcessID: p.ID, Since: p.StartedAt})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var b shown
		if err == nil && json.Unmarshal([]byte(block), &b) == nil && b.Ask != 0 {
			// The words of the reply are the question the owner reads where it waits for them.
			if err := q.SetQuestionText(ctx, db.SetQuestionTextParams{Text: untrusted.Cut(strings.TrimSpace(text), 1000), ID: b.Ask}); err != nil {
				return err
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(s.Now()), TopicID: topic, Role: roleMaster, Text: strings.TrimSpace(text), ProcessID: p.ID}); err != nil {
				return err
			}
		}
		if err := q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(s.Now()), ID: topic}); err != nil {
			return err
		}
		seen, err := q.TurnSeen(ctx, db.TurnSeenParams{TopicID: topic, ProcessID: p.ID})
		if err != nil {
			return err
		}
		return q.SeeChat(ctx, db.SeeChatParams{Seen: seen, TopicID: topic, SessionID: p.SessionID})
	})
	if err != nil {
		slog.Error("chat reply", "process", p.ID, "err", err)
	}
}

// replies keeps the master's replies in their topics and goes on with the messages a topic got while its
// reply ran, once the reply closes or parks.
type replies struct{ s *Service }

func (h replies) Closed(ctx context.Context, p db.Process) { h.goOn(ctx, p); h.s.tell(ctx, p) }

func (h replies) Parked(ctx context.Context, p db.Process) { h.goOn(ctx, p); h.s.tell(ctx, p) }

func (h replies) Submitted(context.Context, db.Process) {}

func (h replies) goOn(ctx context.Context, p db.Process) {
	if topic := h.s.replyOf(ctx, p); topic != 0 {
		h.s.pump(context.WithoutCancel(ctx), topic)
	}
}

// openQuestion is a question of the owner that the process waits on; a message of the topic answers it.
func (s *Service) openQuestion(ctx context.Context, processID int64) int64 {
	asks, err := s.St.Q.ListOpenAsksOfProcess(ctx, processID)
	if err != nil {
		return 0
	}
	for _, q := range asks {
		if q.Kind == ask.KindQuestion && q.State == ask.StateOpen {
			return q.ID
		}
	}
	return 0
}

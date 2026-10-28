package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// replyOf is the topic of a reply process of the built-in master, or 0 for any other process.
func (ch *Service) replyOf(ctx context.Context, p db.Process) int64 {
	proj, err := ch.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Builtin == 0 {
		return 0
	}
	if m, err := docs.Manifest(ctx, ch.St.Q, proj); err != nil || p.Command != m.EventCommand(manifest.OnMessage) {
		return 0
	}
	var in struct{ Topic int64 }
	_ = json.Unmarshal([]byte(p.Input), &in)
	return in.Topic
}

// Turn keeps the final text of a reply turn as the master's message in its topic.
func (h Hook) Turn(ctx context.Context, p db.Process, text string) {
	ch := h.ch
	topic := ch.replyOf(ctx, p)
	if topic == 0 || strings.TrimSpace(text) == "" {
		return
	}
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddMessage(ctx, db.AddMessageParams{CreatedAt: store.Time(ch.Now()), TopicID: topic, Role: roleMaster, Text: strings.TrimSpace(text), ProcessID: p.ID}); err != nil {
			return err
		}
		return q.TouchTopic(ctx, db.TouchTopicParams{Now: store.Time(ch.Now()), ID: topic})
	})
	if err != nil {
		slog.Error("chat reply", "process", p.ID, "err", err)
	}
}

// Hook keeps the master's replies in their topics and goes on with the messages a topic got while its
// reply ran, once the reply closes or parks.
type Hook struct{ ch *Service }

func (h Hook) Closed(ctx context.Context, p db.Process) { h.goOn(ctx, p); h.ch.tell(ctx, p) }

func (h Hook) Parked(ctx context.Context, p db.Process) { h.goOn(ctx, p); h.ch.tell(ctx, p) }

func (h Hook) goOn(ctx context.Context, p db.Process) {
	if topic := h.ch.replyOf(ctx, p); topic != 0 {
		h.ch.pump(context.WithoutCancel(ctx), topic)
	}
}

// openQuestion is a question of the owner that the process waits on; a message of the topic answers it.
func (ch *Service) openQuestion(ctx context.Context, processID int64) int64 {
	asks, err := ch.St.Q.ListOpenAsksOfProcess(ctx, processID)
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

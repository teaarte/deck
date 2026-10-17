package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"deck2/internal/provider"
)

// call sends a request and handles what arrives meanwhile, so the output never backs up.
func (t *turn) call(ctx context.Context, method string, params, out any) error {
	ch, err := t.c.start(method, params)
	if err != nil {
		return err
	}
	for {
		select {
		case m, ok := <-ch:
			return answer(m, ok, method, out)
		case m, ok := <-t.c.incoming:
			if !ok {
				return fmt.Errorf("codex app-server exited before answering %s", method)
			}
			t.handle(ctx, m)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// until reads the output until the turn completes; a stop of deck interrupts the turn first.
func (t *turn) until(ctx context.Context) error {
	cancelled := ctx.Done()
	live := t.req.Live
	// One worker steers the owner's messages in the order they came; after the turn they are refused.
	steers := make(chan provider.Message, 16)
	defer close(steers)
	go func() {
		for m := range steers {
			t.steer(m)
		}
	}()
	var giveUp <-chan time.Time
	for t.completed == 0 {
		select {
		case m, ok := <-live:
			if !ok {
				live = nil
				continue
			}
			select {
			case steers <- m:
			default:
				m.Taken <- false
			}
		case m, ok := <-t.c.incoming:
			if !ok {
				return t.c.ended("codex app-server exited without finishing the turn")
			}
			t.handle(ctx, m)
			if t.limitHit != "" && giveUp == nil {
				giveUp = time.After(15 * time.Second)
			}
		case <-cancelled:
			cancelled = nil
			_, _ = t.c.start("turn/interrupt", TurnInterruptParams{ThreadID: t.thread, TurnID: t.turnID})
			giveUp = time.After(15 * time.Second)
		case <-giveUp:
			if t.limitHit != "" && ctx.Err() == nil {
				return nil
			}
			return ctx.Err()
		}
	}
	// A second completion of the same turn would arrive right behind the first.
	grace := time.After(200 * time.Millisecond)
	for {
		select {
		case m, ok := <-t.c.incoming:
			if !ok {
				return nil
			}
			t.handle(ctx, m)
		case <-grace:
			return nil
		}
	}
}

func (t *turn) emit(e provider.Event) { t.req.Emit(e) }

// handle takes one notification or server request; those of other threads, such as sub-agents, are skipped.
func (t *turn) handle(ctx context.Context, m message) {
	if len(m.ID) > 0 {
		go t.serve(ctx, m)
		return
	}
	var head struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(m.Params, &head)
	if m.Method == "account/rateLimits/updated" {
		var n AccountRateLimitsUpdatedNotification
		if decode(m.Params, &n) {
			t.limits = n.RateLimits
			if reached := string(n.RateLimits.RateLimitReachedType); reached != "" {
				t.emit(provider.Event{Kind: provider.EventRateLimit, Text: reached})
				if t.limitHit == "" && t.turnID != "" && t.completed == 0 {
					// codex waits on a reached limit without ending the turn; deck ends it to go on elsewhere.
					t.limitHit = reached
					_, _ = t.c.start("turn/interrupt", TurnInterruptParams{ThreadID: t.thread, TurnID: t.turnID})
				}
			}
		}
		return
	}
	if head.ThreadID != t.thread || t.thread == "" {
		return
	}
	switch m.Method {
	case "turn/completed":
		var n TurnCompletedNotification
		if decode(m.Params, &n) && n.Turn.ID == t.turnID {
			t.completed++
			if t.completed == 1 {
				t.last = n.Turn
			}
		}
	case "item/started":
		var n ItemStartedNotification
		var it FileChangeThreadItem
		if decode(m.Params, &n) && n.TurnID == t.turnID {
			t.toolStarted(n.Item)
		}
		if decode(m.Params, &n) && n.TurnID == t.turnID && decode(n.Item, &it) && it.Type == "fileChange" {
			t.gwMu.Lock()
			if t.patches == nil {
				t.patches = map[string][]FileUpdateChange{}
			}
			t.patches[it.ID] = it.Changes
			t.gwMu.Unlock()
		}
	case "item/completed":
		var n ItemCompletedNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID {
			t.item(n.Item)
		}
	case "thread/tokenUsage/updated":
		var n ThreadTokenUsageUpdatedNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID && t.req.OnUsage != nil {
			u := n.TokenUsage.Last
			t.req.OnUsage(provider.Usage{Model: t.model, Input: u.InputTokens - u.CachedInputTokens, Output: u.OutputTokens,
				CacheRead: u.CachedInputTokens, CacheCreation: u.CacheWriteInputTokens})
		}
	case "error":
		var n ErrorNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID && !n.WillRetry {
			t.failure = &n.Error
		}
	}
}

// steer puts the owner's message into the turn; a turn that ended meanwhile refuses it.
func (t *turn) steer(m provider.Message) {
	text, _ := json.Marshal(TextUserInput{Type: "text", Text: m.Text})
	ch, err := t.c.start("turn/steer", TurnSteerParams{ThreadID: t.thread, ExpectedTurnID: t.turnID, Input: []UserInput{text}})
	if err != nil {
		m.Taken <- false
		return
	}
	r, ok := <-ch
	m.Taken <- answer(r, ok, "turn/steer", nil) == nil
}

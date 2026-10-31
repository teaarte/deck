package harness

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

type ToldMessage struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	Process int64  `json:"process"`
	Told    *struct {
		ID      int64  `json:"id"`
		Project string `json:"project"`
		Command string `json:"command"`
		State   string `json:"state"`
		Word    string `json:"word"`
		Tone    string `json:"tone"`
		Reason  string `json:"reason"`
		Error   string `json:"error"`
		Open    struct {
			Screen string `json:"screen"`
			Record int64  `json:"record"`
		} `json:"open"`
	} `json:"told"`
}

// Told waits for the message deck writes into topic of a work in state, read in lang.
func (e *Env) Told(topic int64, state, lang string) ToldMessage {
	e.T.Helper()
	var got ToldMessage
	e.Until("deck tells the topic of a work "+state, func() bool {
		var view struct{ Messages []ToldMessage }
		e.ChatRead(map[string]any{"topic": fmt.Sprint(topic), "lang": lang}, &view)
		for _, m := range view.Messages {
			if m.Role == "deck" && m.Told != nil && m.Told.State == state {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

// HandOver is a reply of the master that hands the owner's task to master.work with its topic; a resumed reply
// names no command, and its topic is the one the first named.
func HandOver(ctx context.Context, c *Call, topic *atomic.Int64) provider.Result {
	var in struct{ Topic int64 }
	if m := InputOf.FindStringSubmatch(c.Req.Prompt); m != nil && json.Unmarshal([]byte(m[1]), &in) == nil && in.Topic != 0 {
		topic.Store(in.Topic)
	}
	if out, bad := c.Tool(ctx, "master__work", map[string]any{"task": "count the records", "topic": topic.Load()}); bad {
		return provider.Result{Text: "refused: " + out}
	}
	return provider.Result{Text: "Handed it over."}
}

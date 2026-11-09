package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// shownMessage is a message of the chat with the block it shows.
type shownMessage struct {
	ID    int64  `json:"id"`
	Role  string `json:"role"`
	Text  string `json:"text"`
	Block *struct {
		Type     string          `json:"type"`
		Title    string          `json:"title"`
		Props    json.RawMessage `json:"props"`
		Data     json.RawMessage `json:"data"`
		Answered *struct {
			At     string `json:"at"`
			Choice string `json:"choice"`
		} `json:"answered"`
	} `json:"block"`
}

func shownIn(e *harness.Env, topic int64) []shownMessage {
	var out struct {
		Messages []shownMessage `json:"messages"`
	}
	e.ChatRead(map[string]any{"topic": strconv.FormatInt(topic, 10)}, &out)
	return out.Messages
}

const offerForm = `{"type": "form", "title": "Offer", "submit": "Send", "values": {"price": 100},
  "input": {"type": "object", "required": ["price"], "properties": {
    "price": {"type": "integer", "title": "Price", "minimum": 0, "maximum": 1000, "multipleOf": 10, "examples": [100, 200, 500]},
    "rush": {"type": "boolean", "title": "Rush"}}}}`

// TestTheMasterShowsABlockAndTheOwnerAnswersIt: a reply shows a block under its text, a wrong one refused with why;
// the block keeps the owner's answer to a form or a choice, which comes to the master as the owner's next message.
func TestTheMasterShowsABlockAndTheOwnerAnswersIt(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var told []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		p := c.Req.Prompt
		say := func(s string) { mu.Lock(); told = append(told, s); mu.Unlock() }
		switch {
		case strings.Contains(p, "Price: 120"):
			return provider.Result{Text: "Offered 120."}
		case strings.Contains(p, "pick a price"):
			text, bad := c.Tool(ctx, "chat_block_show", map[string]any{"block": `{"type": "table", "columns": [{"label": "a"}, {"label": "b"}], "rows": [["x"]]}`})
			say(text)
			if !bad {
				return provider.Result{Text: "a table with a short row went through"}
			}
			text, bad = c.Tool(ctx, "chat_block_show", map[string]any{"block": `{"type": "form", "input": {"type": "object", "properties": {"scan": {"type": "string", "format": "file"}}}}`})
			say(text)
			if !bad {
				return provider.Result{Text: "a form with a file went through"}
			}
			text, bad = c.Tool(ctx, "chat_block_show", map[string]any{"block": strings.Replace(offerForm, `"price": 100}`, `"price": "a hundred"}`, 1)})
			say(text)
			if !bad {
				return provider.Result{Text: "a value of a wrong type went through"}
			}
			if text, bad = c.Tool(ctx, "chat_block_show", map[string]any{"block": `{"type": "choice", "options": [{"label": "a"}, {"label": "b"}]}`}); bad {
				return provider.Result{Text: "the first choice was refused: " + text}
			}
			if text, bad = c.Tool(ctx, "chat_block_show", map[string]any{"block": offerForm}); bad || !strings.Contains(text, "took the place") {
				return provider.Result{Text: "the offer was refused: " + text}
			}
			return provider.Result{Text: "Pick the price below."}
		case strings.Contains(p, "which way"):
			if text, bad := c.Tool(ctx, "chat_block_show", map[string]any{"block": `{"type": "choice", "title": "Way", "options": [{"label": "Train", "description": "slow"}, {"label": "Plane"}]}`}); bad {
				return provider.Result{Text: "the choice was refused: " + text}
			}
			return provider.Result{}
		}
		return provider.Result{Text: "ok"}
	}))

	topic := e.Say(0, "pick a price")
	var form shownMessage
	e.Until("the form under the text of the reply, one message in place of the choice shown before it", func() bool {
		ms := shownIn(e, topic)
		for _, m := range ms {
			if m.Role == "master" && m.Block != nil {
				form = m
			}
		}
		return form.Block != nil && form.Text == "Pick the price below." && len(ms) == 2
	})
	mu.Lock()
	if len(told) != 3 || !strings.Contains(told[0], "row 1 has 1 cells for 2 columns") || !strings.Contains(told[1], "takes no files") || !strings.Contains(told[2], "values") {
		t.Fatalf("a wrong block is refused with why: %q", told)
	}
	mu.Unlock()
	var data struct {
		State string `json:"state"`
		Value struct {
			Commands []struct {
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
				Order []string       `json:"order"`
			} `json:"commands"`
			Values map[string]any `json:"values"`
			Send   map[string]any `json:"send"`
		} `json:"value"`
	}
	_ = json.Unmarshal(form.Block.Data, &data)
	if form.Block.Type != "form" || form.Block.Title != "Offer" || data.State != "ready" || len(data.Value.Commands) != 1 ||
		data.Value.Commands[0].Name != "deck.chat_answer" || data.Value.Values["price"] != float64(100) ||
		data.Value.Send["command"] != "deck.chat_answer" || data.Value.Send["input"] != "values" || data.Value.Send["label"] != "Send" ||
		strings.Join(data.Value.Commands[0].Order, ",") != "price,rush" {
		t.Fatalf("the form asks by its fields and answers through chat_answer: %+v %s", form.Block, form.Block.Data)
	}

	e.Refused("deck.chat_answer", map[string]any{"message": form.ID, "values": `{"price": 1234}`}, http.StatusUnprocessableEntity, "values")
	e.Refused("deck.chat_answer", map[string]any{"message": form.ID, "choice": "Train"}, http.StatusUnprocessableEntity, "values")
	e.Refused("deck.chat_block_show", map[string]any{"block": offerForm}, http.StatusUnprocessableEntity, "only a reply of the master")
	e.Must("deck.chat_answer", map[string]any{"message": form.ID, "values": `{"price": 120, "rush": true}`}, nil)
	e.Refused("deck.chat_answer", map[string]any{"message": form.ID, "values": `{"price": 130}`}, http.StatusConflict, "answered already")

	e.Until("the answer as the owner's message and the master acting on it", func() bool {
		ms := shownIn(e, topic)
		n := len(ms)
		return n == 4 && ms[2].Role == "owner" && ms[2].Text == "Offer\nPrice: 120\nRush: Yes" && ms[3].Text == "Offered 120."
	})
	ms := shownIn(e, topic)
	var done struct {
		Value struct {
			Values   map[string]any `json:"values"`
			Send     map[string]any `json:"send"`
			Readonly []string       `json:"readonly"`
		} `json:"value"`
	}
	_ = json.Unmarshal(ms[1].Block.Data, &done)
	if ms[1].Block.Answered == nil || done.Value.Send != nil || done.Value.Values["price"] != float64(120) || len(done.Value.Readonly) != 2 {
		t.Fatalf("the block keeps the answer and takes no other: %s", ms[1].Block.Data)
	}

	e.Say(topic, "which way")
	var choice shownMessage
	e.Until("the options of a choice, each answering it", func() bool {
		for _, m := range shownIn(e, topic) {
			if m.Block != nil && m.Block.Type == "choice" {
				choice = m
			}
		}
		return choice.Block != nil
	})
	var opts struct {
		Value struct {
			Options []struct {
				Label  string         `json:"label"`
				Action map[string]any `json:"action"`
			} `json:"options"`
		} `json:"value"`
	}
	_ = json.Unmarshal(choice.Block.Data, &opts)
	if len(opts.Value.Options) != 2 || opts.Value.Options[1].Action["command"] != "deck.chat_answer" || opts.Value.Options[1].Action["label"] != "Plane" {
		t.Fatalf("each option is a button that answers: %s", choice.Block.Data)
	}
	e.Refused("deck.chat_answer", map[string]any{"message": choice.ID, "choice": "Bus"}, http.StatusUnprocessableEntity, "not an option")
	e.Must("deck.chat_answer", map[string]any{"message": choice.ID, "choice": "Plane"}, nil)
	e.Until("the option picked as the owner's message, the block keeping it", func() bool {
		ms := shownIn(e, topic)
		for i, m := range ms {
			if m.ID == choice.ID {
				return m.Block.Answered != nil && m.Block.Answered.Choice == "Plane" && i+1 < len(ms) && ms[i+1].Role == "owner" && ms[i+1].Text == "Way: Plane"
			}
		}
		return false
	})
}

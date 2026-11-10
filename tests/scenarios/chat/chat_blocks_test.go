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
    "rush": {"type": "boolean", "title": "Rush", "default": false}}}}`

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
		data.Value.Values["price"] != float64(100) || data.Value.Values["rush"] != false ||
		data.Value.Send["command"] != "deck.ask_answer" || data.Value.Send["input"] != "answer" || data.Value.Send["label"] != "Send" ||
		strings.Join(data.Value.Commands[0].Order, ",") != "price,rush" {
		t.Fatalf("the form asks by its fields and answers its question: %+v %s", form.Block, form.Block.Data)
	}
	asked := waiting(e)
	if len(asked) != 1 || asked[0].Kind != "question" || asked[0].Project != "master" || asked[0].ID != data.Value.Send["params"].(map[string]any)["ask"] ||
		len(asked[0].Payload.Questions) != 1 || asked[0].Payload.Questions[0].Question != "Pick the price below." || asked[0].Payload.Questions[0].Form == nil {
		t.Fatalf("the form is one question that waits for the owner, in the words of the reply, the choice it replaced gone: %+v", asked)
	}
	id := int64(asked[0].ID)

	e.Refused("deck.ask_answer", map[string]any{"ask": id, "answer": `{"price": 1234}`}, http.StatusUnprocessableEntity, "values")
	e.Refused("deck.ask_answer", map[string]any{"ask": id, "answer": "Train"}, http.StatusUnprocessableEntity, "JSON object")
	e.Refused("deck.chat_block_show", map[string]any{"block": offerForm}, http.StatusUnprocessableEntity, "only a reply of the master")
	e.Must("deck.ask_answer", map[string]any{"ask": id, "answer": `{"price": 120, "rush": true}`}, nil)
	e.Refused("deck.ask_answer", map[string]any{"ask": id, "answer": `{"price": 130}`}, http.StatusConflict, "already answered")

	e.Until("the answer where the owner waits for, as the owner's message, and the master acting on it", func() bool {
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
		t.Fatalf("the block in the chat keeps the answer and takes no other: %s", ms[1].Block.Data)
	}
	if left := waiting(e); len(left) != 0 {
		t.Fatalf("an answered question waits no more: %+v", left)
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
	if len(opts.Value.Options) != 2 || opts.Value.Options[1].Action["command"] != "deck.ask_answer" || opts.Value.Options[1].Action["label"] != "Plane" {
		t.Fatalf("each option is a button that answers: %s", choice.Block.Data)
	}
	asked = waiting(e)
	if len(asked) != 1 || len(asked[0].Payload.Questions) != 1 || len(asked[0].Payload.Questions[0].Options) != 2 || asked[0].Payload.Questions[0].Header != "Way" {
		t.Fatalf("the choice waits for the owner with its options: %+v", asked)
	}
	params := opts.Value.Options[1].Action["params"].(map[string]any)
	e.Must("deck.ask_answer", map[string]any{"ask": params["ask"], "answer": params["answer"]}, nil)
	e.Until("the option picked in the chat as the owner's message, the block keeping it", func() bool {
		ms := shownIn(e, topic)
		for i, m := range ms {
			if m.ID == choice.ID {
				return m.Block.Answered != nil && m.Block.Answered.Choice == "Plane" && i+1 < len(ms) && ms[i+1].Role == "owner" && ms[i+1].Text == "Way: Plane"
			}
		}
		return false
	})
	if left := waiting(e); len(left) != 0 {
		t.Fatalf("a question answered in the chat waits no more: %+v", left)
	}

	e.Say(topic, "which way")
	e.Until("another choice waits", func() bool { return len(waiting(e)) == 1 })
	e.Say(topic, "by bus, I said")
	if left := waiting(e); len(left) != 0 {
		t.Fatalf("a question the owner wrote past in words waits no more: %+v", left)
	}
	ms = shownIn(e, topic)
	for _, m := range ms {
		if m.Block != nil && m.Block.Answered == nil && strings.Contains(string(m.Block.Data), "ask_answer") {
			t.Fatalf("its options answer nothing now: %s", m.Block.Data)
		}
	}

	e.Say(topic, "which way")
	e.Until("a choice waits again", func() bool { return len(waiting(e)) == 1 })
	asked = waiting(e)
	e.Must("deck.ask_answer", map[string]any{"ask": int64(asked[0].ID), "answer": `{"Way": "Bus"}`}, nil)
	e.Until("the owner's own words picked on Today, as the picker sends them", func() bool {
		ms := shownIn(e, topic)
		return ms[len(ms)-1].Text == "Way: Bus" || len(ms) > 1 && ms[len(ms)-2].Text == "Way: Bus"
	})

	e.Say(topic, "which way")
	e.Until("one more choice waits", func() bool { return len(waiting(e)) == 1 })
	gone := waiting(e)[0]
	e.Must("deck.chat_topic_delete", map[string]any{"topic": topic}, nil)
	if left := waiting(e); len(left) != 0 {
		t.Fatalf("the questions of a deleted topic wait no more: %+v", left)
	}
	e.Refused("deck.ask_answer", map[string]any{"ask": int64(gone.ID), "answer": "Plane"}, http.StatusConflict, "already answered")
}

// waitingAsk is a question as asks_list, the source of what waits for the owner on Today, gives it.
type waitingAsk struct {
	ID      float64 `json:"id"`
	Kind    string  `json:"kind"`
	Project string  `json:"project"`
	Payload struct {
		Questions []struct {
			Question string           `json:"question"`
			Header   string           `json:"header"`
			Options  []map[string]any `json:"options"`
			Form     map[string]any   `json:"form"`
		} `json:"questions"`
	} `json:"payload"`
}

func waiting(e *harness.Env) []waitingAsk {
	var out []waitingAsk
	e.Must("deck.asks_list", map[string]any{}, &out)
	return out
}

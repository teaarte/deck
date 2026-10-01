package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

var cardOf = regexp.MustCompile(`about card (\d+) of project`)

func TestInterviewTopicWritesTheSpecIntoTheCard(t *testing.T) {
	var card int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if m := cardOf.FindStringSubmatch(c.Req.Prompt); m != nil {
			_, _ = fmt.Sscan(m[1], &card)
		}
		switch {
		case strings.Contains(c.Req.Prompt, "Interview me"):
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which format?",
				"options": []any{map[string]any{"label": "CSV"}, map[string]any{"label": "PDF"}}}}})
			answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
			spec := fmt.Sprintf("Export the report.\n\n## Spec\n\nGoal: the weekly report as a file.\nDetails: format %v.", answers["Which format?"])
			if out, bad := c.Tool(ctx, "card_update", map[string]any{"card": card, "text": spec}); bad {
				return provider.Result{Text: "refused: " + out}
			}
			return provider.Result{Text: "The spec is in the card. Shall I create these cards? 1. Build the export. 2. Add the button (waits 1)."}
		case strings.Contains(c.Req.Prompt, "yes, create them"):
			var first struct{ ID int64 }
			out, _ := c.Tool(ctx, "card_create", map[string]any{"project": "work", "title": "Build the export", "text": fmt.Sprint("For card #", card)})
			_ = json.Unmarshal([]byte(out), &first)
			c.Tool(ctx, "card_create", map[string]any{"project": "work", "title": "Add the button", "waits": []int64{first.ID}})
			return provider.Result{Text: "Created two cards."}
		}
		return provider.Result{Text: "?"}
	}, func(c *app.Config) { c.QuestionWait = time.Minute })
	e.createProject("work", boardWork)
	c := e.card(map[string]any{"project": "work", "title": "Export the report", "text": "Export the report."})
	var out struct{ Topic int64 }
	e.must("deck.chat_send", map[string]any{"card": c.ID, "text": "Interview me about this card and write the spec into it."}, &out)
	var ts []chatTopic
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 1 || ts[0].Card != c.ID {
		t.Fatalf("the interview is a topic about the card: %+v", ts)
	}
	q := e.waitAsk()
	if q.Kind != "question" || !strings.Contains(q.Text, "Which format?") {
		t.Fatalf("the question waits in waiting for you: %+v", q)
	}
	e.say(out.Topic, "CSV")
	e.masterSaid(out.Topic, 1)
	var got struct{ Text string }
	e.must("deck.card_get", map[string]any{"card": c.ID}, &got)
	if !strings.Contains(got.Text, "## Spec") || !strings.Contains(got.Text, "format CSV") || !strings.HasPrefix(got.Text, "Export the report.") {
		t.Fatalf("the spec is in the text of the card: %q", got.Text)
	}
	if titles := e.cardTitles("work"); len(titles) != 1 {
		t.Fatalf("no cards before the owner's yes: %v", titles)
	}
	e.say(out.Topic, "yes, create them")
	e.masterSaid(out.Topic, 2)
	var board struct {
		Lanes []struct {
			Cards []struct {
				ID    int64
				Title string
				Waits []int64
			} `json:"cards"`
		} `json:"lanes"`
	}
	e.must("deck.cards_list", map[string]any{"project": "work"}, &board)
	cards := board.Lanes[0].Cards
	if len(cards) != 3 || cards[2].Title != "Add the button" || len(cards[2].Waits) != 1 || cards[2].Waits[0] != cards[1].ID {
		t.Fatalf("after the yes the split is cards that wait for each other: %+v", cards)
	}
	if len(e.suggestions()) != 0 {
		t.Fatal("the split is made, not suggested")
	}
}

func TestMasterPutsATaskIntoTheRightProject(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		list, _ := c.Tool(ctx, "projects_list", map[string]any{})
		var ps []struct{ Name, Description string }
		_ = json.Unmarshal([]byte(list), &ps)
		if strings.Contains(c.Req.Prompt, "move it to garden") {
			var board struct {
				Lanes []struct {
					Cards []struct{ ID int64 } `json:"cards"`
				} `json:"lanes"`
			}
			out, _ := c.Tool(ctx, "cards_list", map[string]any{"project": "groceries"})
			_ = json.Unmarshal([]byte(out), &board)
			c.Tool(ctx, "card_create", map[string]any{"project": "garden", "title": "Buy a rake"})
			c.Tool(ctx, "card_delete", map[string]any{"card": board.Lanes[0].Cards[0].ID})
			return provider.Result{Text: "Moved it to garden."}
		}
		for _, p := range ps {
			if strings.Contains(p.Description, "shopping") {
				c.Tool(ctx, "card_create", map[string]any{"project": p.Name, "title": "Buy a rake"})
				return provider.Result{Text: "Put it into " + p.Name + ". Move it?"}
			}
		}
		return provider.Result{Text: "no project"}
	})
	e.must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# The shopping list of the household.\n"}, nil)
	e.must("deck.project_create", map[string]any{"name": "garden", "prompt": "# Work in the garden.\n"}, nil)
	topic := e.say(0, "buy a rake")
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into groceries. Move it?" || len(e.cardTitles("groceries")) != 1 {
		t.Fatalf("the master picks the project by the first line of its md: %+v", said)
	}
	if !strings.Contains(e.Fake.Calls()[0].Prompt, "dispatch") && len(e.Fake.Calls()[0].Skills) == 0 {
		t.Fatal("the master has the dispatch skill")
	}
	if _, ok := e.Fake.Calls()[0].Skills["dispatch/SKILL.md"]; !ok {
		t.Fatalf("the master gets the dispatch skill: %v", e.Fake.Calls()[0].Skills)
	}
	e.say(topic, "no, move it to garden")
	e.masterSaid(topic, 2)
	if g, gr := e.cardTitles("garden"), e.cardTitles("groceries"); len(g) != 1 || len(gr) != 0 {
		t.Fatalf("move puts the card into the other project: %v %v", g, gr)
	}
}

package chat

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

var heldAsk = regexp.MustCompile(`\(ask (\d+)\)`)

// openOf are the asks of a process that wait for the owner now.
func openOf(e *harness.Env, process int64) []harness.Ask {
	var out []harness.Ask
	for _, a := range e.Asks() {
		if a.Process == process {
			out = append(out, a)
		}
	}
	return out
}

// toldOf is the message deck wrote into the topic of the work, read in Russian.
func toldOf(e *harness.Env, topic, process int64) harness.ToldMessage {
	var view struct{ Messages []harness.ToldMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic), "lang": "ru"}, &view)
	for _, m := range view.Messages {
		if m.Role == "deck" && m.Process == process {
			return m
		}
	}
	return harness.ToldMessage{}
}

// A work that waits for the owner's button and asks about it in the topic twice is one wait: the owner sees the
// request alone, and once they press it the topic no longer says the work waits for them.
func TestAWorkWaitingForARequestAsksAboutItOnce(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	var asked atomic.Value
	goOn := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "the owner has responded"):
			select {
			case <-goOn:
			case <-ctx.Done():
			}
			return provider.Result{Text: "Models set, the rest ran."}
		case c.Command() != "work":
			return harness.HandOver(ctx, c, &from)
		}
		said, _ := c.Tool(ctx, "assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/qwen"}})
		m := heldAsk.FindStringSubmatch(said)
		if m == nil {
			return provider.Result{Text: "not held: " + said}
		}
		var denies []string
		for range 2 {
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{
				"question": "Нажали кнопку запроса " + m[1] + " в кокпите? Продолжать?",
				"options":  []map[string]any{{"label": "Нажал"}, {"label": "Только NVIDIA"}}}}})
			denies = append(denies, fmt.Sprint(d["behavior"], ": ", d["message"]))
		}
		asked.Store(denies)
		return provider.Result{Text: "Waiting for the button of request " + m[1] + "."}
	}))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Project("films", harness.PlainManifest)

	defer close(goOn)
	topic := e.Say(0, "set the models of films")
	waits := e.Told(topic, "waiting", "ru")
	denies, _ := asked.Load().([]string)
	if len(denies) != 2 {
		t.Fatalf("the work asked twice: %v", denies)
	}
	for _, d := range denies {
		if !strings.HasPrefix(d, "deny: ") || !strings.Contains(d, "already waits for the owner") {
			t.Fatalf("a question about a request that waits is not a second wait: %s", d)
		}
	}
	open := openOf(e, waits.Process)
	if len(open) != 1 || open[0].Kind != "call" {
		t.Fatalf("the owner sees one thing the work waits for, its request: %+v", open)
	}
	if !strings.HasPrefix(waits.Told.Word, "ждёт") || waits.Told.Tone != "warn" {
		t.Fatalf("the topic says the work waits for the owner: %+v", waits.Told)
	}

	e.Answer(open[0].ID, "allow")
	if got := e.Assigned("films", "text"); len(got) != 1 || got[0] != "openrouter/qwen" {
		t.Fatalf("the owner's yes makes the call: %v", got)
	}
	e.WaitState(waits.Process, "running")
	now := toldOf(e, topic, waits.Process)
	if now.Told == nil || now.Told.State != "running" || strings.HasPrefix(now.Told.Word, "ждёт") || now.Told.Tone == "warn" {
		t.Fatalf("once the owner pressed the button the topic does not say the work waits: %+v", now.Told)
	}
	var strip struct {
		Running []struct {
			ID    int64 `json:"id"`
			Waits bool  `json:"waits"`
		} `json:"running"`
	}
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic), "lang": "ru"}, &strip)
	for _, r := range strip.Running {
		if r.ID == waits.Process && r.Waits {
			t.Fatalf("the strip of the topic does not say the work waits: %+v", strip.Running)
		}
	}
	goOn <- struct{}{}
	e.WaitState(waits.Process, "done")
}

// A question a work asks again while the owner has not answered it is the same question: the owner answers one.
func TestAQuestionAskedAgainIsOneQuestion(t *testing.T) {
	t.Parallel()
	var step atomic.Int32
	var got atomic.Value
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			return provider.Result{Text: "ok"}
		}
		ask := func(q string) string {
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": q,
				"options": []map[string]any{{"label": "tasks"}, {"label": "notes"}}}}})
			step.Add(1)
			return fmt.Sprint(d["behavior"], d["updatedInput"])
		}
		var said []string
		for _, q := range []string{"Which collection?", "Which collection?"} {
			said = append(said, ask(q))
		}
		got.Store(said)
		return provider.Result{Text: "Counted."}
	}), harness.WithClock())
	films := e.Project("films", harness.PlainManifest)
	id := e.Run(films, "work")

	questions := func() []harness.Ask {
		var out []harness.Ask
		for _, a := range openOf(e, id) {
			if a.Kind == "question" {
				out = append(out, a)
			}
		}
		return out
	}
	first := e.WaitAsk()
	e.PassWaits("the work to give up on its first question", func() bool { return step.Load() == 1 })
	e.Until("the work to ask the same again", func() bool { return e.App.Asks.Live(first.ID) })
	if qs := questions(); len(qs) != 1 || qs[0].ID != first.ID {
		t.Fatalf("a question asked again is the open one: %+v", qs)
	}
	e.Answer(first.ID, "tasks")
	e.Until("the answer to reach the work", func() bool { return step.Load() == 2 })

	e.WaitState(id, "done")
	said, _ := got.Load().([]string)
	if len(said) != 2 || !strings.HasPrefix(said[0], "deny") || !strings.HasPrefix(said[1], "allow") || !strings.Contains(said[1], "tasks") {
		t.Fatalf("the answers reach the calls that wait: %v", said)
	}
}

// A request of a work that sets all the places of its earlier one takes its place: the owner sees the later alone,
// which says what it replaces, and the work hears that the earlier was closed.
func TestALaterRequestOfTheSamePlacesReplacesTheEarlier(t *testing.T) {
	t.Parallel()
	var held atomic.Value
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			return provider.Result{Text: "done"}
		}
		a, _ := c.Tool(ctx, "assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/qwen"}})
		b, _ := c.Tool(ctx, "assignment_set", map[string]any{"assignments": `[{"role": "text", "project": "films", "models": ["openrouter/llama"]},
			{"role": "text", "project": "films", "command": "work", "models": ["openrouter/qwen"]}]`})
		held.Store([]string{a, b})
		return provider.Result{Text: "Waiting for the owner."}
	}))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "llama"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	films := e.Project("films", harness.PlainManifest)
	id := e.Run(films, "work")
	e.WaitState(id, "waiting:answer")

	said, _ := held.Load().([]string)
	var ids []int64
	for _, s := range said {
		if m := heldAsk.FindStringSubmatch(s); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			ids = append(ids, n)
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("both calls wait for the owner: %v", said)
	}
	if s := e.AskState(ids[0]); s != "answered" {
		t.Fatalf("the earlier request is closed: %s", s)
	}
	var rows []struct {
		ID    int64  `json:"id"`
		Where string `json:"where"`
	}
	e.Must("deck.asks_list", map[string]any{"lang": "ru"}, &rows)
	if len(rows) != 1 || rows[0].ID != ids[1] || !strings.Contains(rows[0].Where, fmt.Sprintf("Заменяет запрос %d", ids[0])) {
		t.Fatalf("the owner sees the later request alone, saying what it replaces: %+v", rows)
	}
	e.Answer(ids[1], "deny")
	e.WaitState(id, "done")
	heard := false
	for _, c := range e.Fake.Calls() {
		heard = heard || strings.Contains(c.Prompt, fmt.Sprintf("your later call, ask %d, took the place of this one", ids[1]))
	}
	if !heard {
		t.Fatalf("the work hears its earlier call was replaced: %+v", e.Fake.Calls())
	}
}

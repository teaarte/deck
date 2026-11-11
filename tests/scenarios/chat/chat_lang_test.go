package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// spoken is what the agents of a topic were told about its language, by command, and what the reply heard back.
type spoken struct {
	mu      sync.Mutex
	prompts map[string][]string
	marked  []string
	refused string
}

func (s *spoken) keep(command, prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompts == nil {
		s.prompts = map[string][]string{}
	}
	s.prompts[command] = append(s.prompts[command], prompt)
}

func (s *spoken) of(command string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts[command]...)
}

// langAgent is a master whose reply marks the language of the owner's message, as a model reads it, and hands the
// task to a work that asks the owner for a button.
func langAgent(said *spoken, from *atomic.Int64) harness.Option {
	return harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		cmd := c.Command()
		if cmd == "" {
			cmd = "reply"
		}
		said.keep(cmd, c.Req.Prompt)
		switch cmd {
		case "work":
			out, bad := c.Tool(ctx, "chat_topic_lang_set", map[string]any{"lang": "de"})
			said.mu.Lock()
			said.refused = fmt.Sprint(bad, " ", out)
			said.mu.Unlock()
			out, _ = c.Tool(ctx, "assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/qwen"}})
			return provider.Result{Text: "held: " + out}
		}
		cmd = "reply"
		lang := "en"
		if strings.ContainsAny(c.Req.Prompt, "абвгдеёжзиклмнопрстуфхцчшщыэюя") && strings.Contains(c.Req.Prompt, "поставь") {
			lang = "ru"
		}
		if !strings.Contains(c.Req.Prompt, "the language "+map[string]string{"en": "English", "ru": "Russian"}[lang]) {
			out, bad := c.Tool(ctx, "chat_topic_lang_set", map[string]any{"lang": lang})
			said.mu.Lock()
			said.marked = append(said.marked, fmt.Sprint(bad, " ", out))
			said.mu.Unlock()
		}
		return harness.HandOver(ctx, c, from)
	})
}

// askIn is the request of a process as asks_list reads it in lang.
func askIn(e *harness.Env, process int64, lang string) map[string]any {
	var rows []map[string]any
	e.Must("deck.asks_list", map[string]any{"lang": lang}, &rows)
	for _, r := range rows {
		if n, _ := r["process"].(float64); int64(n) == process {
			return r
		}
	}
	return nil
}

func cyrillic(s string) bool { return strings.ContainsAny(s, "абвгдеёжзиклмнопрстуфхцчшщыэюяАБВГДЕЖЗИКЛМНОПРСТУФХЦЧШЩЭЮЯ") }

func langEnv(t *testing.T, setting string) (*harness.Env, *spoken) {
	said := &spoken{}
	var from atomic.Int64
	e := harness.New(t, langAgent(said, &from))
	e.Must("deck.setting_set", map[string]any{"name": "language", "value": setting}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Project("films", harness.PlainManifest)
	return e, said
}

// With English settings the owner writes in Russian: the reply marks the topic Russian, and the work it hands over,
// the request of that work and the words deck says of it in the topic are Russian, whatever the client reads.
func TestATopicSpeaksTheLanguageTheOwnerWritesIn(t *testing.T) {
	t.Parallel()
	e, said := langEnv(t, "en")

	topic := e.Say(0, "поставь модель для films")
	waits := e.Told(topic, "waiting_answer", "en")
	if len(said.marked) != 1 || !strings.HasPrefix(said.marked[0], "false ") {
		t.Fatalf("the reply marks the topic Russian once: %q", said.marked)
	}
	reply := said.of("reply")
	if len(reply) != 1 || !strings.Contains(reply[0], "deck gives this topic the language English") {
		t.Fatalf("before the mark the reply is given the language of the settings: %q", reply)
	}
	work := said.of("work")
	if len(work) != 1 || !strings.Contains(work[0], "in Russian, the language deck gives") {
		t.Fatalf("the work of the topic writes for the owner in its language: %q", work)
	}
	if !strings.HasPrefix(waits.Told.Word, "ждёт") {
		t.Fatalf("deck's words of the work in the topic are Russian for an English client: %+v", waits.Told)
	}
	r := askIn(e, waits.Process, "en")
	if r == nil || !cyrillic(fmt.Sprint(r["allow"])) || !cyrillic(fmt.Sprint(r["what"])) || !cyrillic(fmt.Sprint(r["where"])) ||
		!cyrillic(fmt.Sprint(r["actions"])) || strings.Contains(fmt.Sprint(r["actions"]), "Deny") {
		t.Fatalf("the request of the work and its buttons are Russian for an English client: %+v", r)
	}
	said.mu.Lock()
	if !strings.HasPrefix(said.refused, "true ") || !strings.Contains(said.refused, "only a reply of the master") {
		t.Fatalf("a work of the master marks no language of a topic: %s", said.refused)
	}
	said.mu.Unlock()

	e.Say(topic, "ещё поставь")
	e.Until("the second reply", func() bool { return len(said.of("reply")) == 2 })
	if got := said.of("reply")[1]; !strings.Contains(got, "deck gives this topic the language Russian") {
		t.Fatalf("the next turn is given the topic's language: %s", got)
	}
	if len(said.marked) != 1 {
		t.Fatalf("a topic already Russian is not marked again: %q", said.marked)
	}
}

// With Russian settings the owner writes in English: what comes of the topic is English, for a Russian client too;
// the last message decides.
func TestATopicInEnglishUnderRussianSettings(t *testing.T) {
	t.Parallel()
	e, said := langEnv(t, "ru")

	topic := e.Say(0, "set a model for films")
	waits := e.Told(topic, "waiting_answer", "ru")
	if len(said.marked) != 1 || !strings.HasPrefix(said.marked[0], "false ") {
		t.Fatalf("the reply marks the topic English: %q", said.marked)
	}
	if reply := said.of("reply"); !strings.Contains(reply[0], "deck gives this topic the language Russian") {
		t.Fatalf("before the mark the reply is given the language of the settings: %q", reply)
	}
	if work := said.of("work"); len(work) != 1 || !strings.Contains(work[0], "in English, the language deck gives") {
		t.Fatalf("the work of the topic writes in English: %q", work)
	}
	if strings.HasPrefix(waits.Told.Word, "ждёт") || !strings.HasPrefix(waits.Told.Word, "waits") {
		t.Fatalf("deck's words of the work in the topic are English for a Russian client: %+v", waits.Told)
	}
	r := askIn(e, waits.Process, "ru")
	if r == nil || cyrillic(fmt.Sprint(r["allow"])) || cyrillic(fmt.Sprint(r["what"])) || cyrillic(fmt.Sprint(r["actions"])) {
		t.Fatalf("the request of the work is English for a Russian client: %+v", r)
	}

	e.Say(topic, "поставь ещё одну")
	e.Until("the reply to the Russian message", func() bool { return len(said.of("reply")) == 2 })
	e.Until("the topic marked Russian again", func() bool {
		said.mu.Lock()
		defer said.mu.Unlock()
		return len(said.marked) == 2
	})
	if r := askIn(e, waits.Process, "en"); r == nil || !cyrillic(fmt.Sprint(r["allow"])) {
		t.Fatalf("the last message decides: the request is Russian now: %+v", r)
	}
}

// Without a topic, as a run of a schedule, an agent writes in the language of the settings.
func TestWithoutATopicTheSettingsSpeak(t *testing.T) {
	t.Parallel()
	said := &spoken{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		said.keep(c.Command(), c.Req.Prompt)
		return provider.Result{Text: "done"}
	}))
	films := e.Project("films", harness.PlainManifest)
	for _, c := range []struct{ setting, name string }{{"ru", "Russian"}, {"en", "English"}} {
		e.Must("deck.setting_set", map[string]any{"name": "language", "value": c.setting}, nil)
		id := e.Run(films, "work")
		e.WaitState(id, "done")
		got := said.of("work")
		if !strings.Contains(got[len(got)-1], "in "+c.name+", the language deck gives") {
			t.Fatalf("a run without a topic writes in the language of the settings %s: %s", c.setting, got[len(got)-1])
		}
	}
}

// With the settings following the device, whatever language the client sending reads, deck gives the reply no
// language: the reply marks the one of the message, and the work and pushes of the topic follow it.
func TestUnderAutoSettingsTheReplyMarksTheTopic(t *testing.T) {
	t.Parallel()
	e, said := langEnv(t, "auto")

	var sent struct{ Topic int64 }
	e.In("ru").Must("deck.chat_send", map[string]any{"text": "поставь модель для films"}, &sent)
	topic := sent.Topic
	waits := e.Told(topic, "waiting_answer", "en")
	if reply := said.of("reply"); len(reply) != 1 || !strings.Contains(reply[0], "deck knows no language of this topic yet") ||
		strings.Contains(reply[0], "deck gives this topic the language") {
		t.Fatalf("the reply is given no language of a client: %q", reply)
	}
	if len(said.marked) != 1 || !strings.HasPrefix(said.marked[0], "false ") {
		t.Fatalf("the reply marks the topic: %q", said.marked)
	}
	if work := said.of("work"); len(work) != 1 || !strings.Contains(work[0], "in Russian, the language deck gives") {
		t.Fatalf("the work of the marked topic writes in Russian: %q", work)
	}
	if !strings.HasPrefix(waits.Told.Word, "ждёт") {
		t.Fatalf("deck's words of the work are Russian: %+v", waits.Told)
	}
}

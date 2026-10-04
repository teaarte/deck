package scenarios

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

// TestRealBrowserMasterChat talks to the master from the side panel in the real browser: a topic and its reply,
// a question answered in the feed, a turn stopped, and the panel on the whole screen of a phone.
func TestRealBrowserMasterChat(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "which list"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "cards_list", Text: "{}"})
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?",
				"options": []any{map[string]any{"label": "shop"}, map[string]any{"label": "market"}}}}})
			answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
			return provider.Result{Text: fmt.Sprint("Picked ", answers["Which list?"])}
		case strings.Contains(c.Req.Prompt, "think long"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "records_list", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Put it into **groceries** as card #1."}
	}, func(c *app.Config) { c.QuestionWait = 2 * time.Minute })
	e.must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n"}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/groceries/board")
	b.until("the board", `document.querySelector('[data-lane]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat beside the screen", `document.querySelector('[data-chat-panel] [data-chat-input]') && document.querySelector('[data-lane]')`)
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	say("we are out of milk")
	b.until("the master's reply in the feed", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.querySelector('strong')?.innerText === 'groceries')`)
	b.shot("chat-reply")
	b.until("the screen the message goes with", `document.querySelector('[data-context]')?.innerText.includes('groceries')`)
	b.eval(`(document.querySelector('[data-context] button').click(), '')`)
	b.until("a message without the screen", `document.querySelector('[data-context-off]')`)
	b.eval(`(document.querySelector('[data-context-off]').click(), '')`)
	b.until("the screen back", `document.querySelector('[data-context]')`)
	ms := e.chatMessages(1)
	if len(ms) != 2 || !strings.Contains(string(ms[0].Screen), `"project":"groceries"`) || !strings.Contains(string(ms[0].Screen), `"screen":"board"`) {
		t.Fatalf("the message carries the screen the owner is on: %+v", ms)
	}

	say("which list then?")
	b.until("the question in the feed", `document.querySelector('[data-question]')?.innerText.includes('Which list?')`)
	b.until("the turn with its calls", `document.querySelector('[data-turn] [data-turn-line]')?.innerText.includes('cards_list')`)
	b.eval(`([...document.querySelectorAll('[data-question] input[type=checkbox], [data-question] input[type=radio]')].find((i) => i.value === 'market').click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-question] button')].find((x) => !x.disabled && x.innerText.trim() !== '').click(), '')`)
	b.until("the reply after the answer", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('Picked market'))`)
	b.until("the question gone", `!document.querySelector('[data-question]')`)

	say("think long about it")
	b.until("the running turn with stop", `document.querySelector('[data-turn="running"] [data-verb="stop"]')`)
	b.shot("chat-turn")
	b.eval(`(document.querySelector('[data-turn] [data-verb="stop"]').click(), '')`)
	b.until("the stopped reply", `document.querySelector('[data-reply-state="stopped"]') && !document.querySelector('[data-turn]')`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the chat on the whole phone screen", `(() => { const i = document.querySelector('[data-chat-input]'); if (!i) return false;
		const r = i.getBoundingClientRect(); return r.width > 300 && !document.querySelector('[data-chat-panel]') })()`)
	b.until("the newest topic open on the phone", `document.querySelector('[data-message="master"]')`)
	b.eval(`(document.querySelector('[data-new-topic]').click(), '')`)
	b.until("a new topic", `!document.querySelector('[data-message]')`)
	say("from the phone")
	b.until("the reply on the phone", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	b.shot("chat-phone")
	var ts []chatTopic
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 2 || ts[0].Title != "from the phone" {
		t.Fatalf("the phone opened a topic of its own: %+v", ts)
	}
}

// TestRealBrowserVoice records the microphone in the chat of the real browser: the recording goes to the whisper
// server as WAV, and its text lands in the input, where the owner sends it.
func TestRealBrowserVoice(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	heard := make(chan []byte, 1)
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(f)
		select {
		case heard <- b:
		default:
		}
		_, _ = w.Write([]byte(`{"text": "buy oat milk"}`))
	}))
	t.Cleanup(whisper.Close)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "Put it into groceries."}
	})
	e.must("deck.stt_set", map[string]any{"url": whisper.URL + "/inference"}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(() => { navigator.mediaDevices.getUserMedia = async () => { const ctx = new AudioContext(); const o = ctx.createOscillator();
		const dst = ctx.createMediaStreamDestination(); o.connect(dst); o.start(); return dst.stream; }; return '' })()`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the mic", `document.querySelector('[data-mic]')`)
	b.eval(`(document.querySelector('[data-mic]').click(), '')`)
	b.until("the recording", `document.querySelector('[data-mic-stop]')`)
	time.Sleep(1500 * time.Millisecond)
	b.eval(`(document.querySelector('[data-mic-stop]').click(), '')`)
	b.until("the text in the input", `document.querySelector('[data-chat-input]').value === 'buy oat milk'`)
	select {
	case wav := <-heard:
		if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wav[24:28]) != 16000 {
			t.Fatalf("the recording is 16 kHz WAV: %d bytes %q", len(wav), wav[:min(len(wav), 12)])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the recording did not reach whisper")
	}
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the reply", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	b.shot("chat-voice")
}

// TestRealBrowserInterviewFromACard edits a card and presses Interview: the edit is saved, the chat opens on a topic about
// the card, and the master's question waits in the feed.
func TestRealBrowserInterviewFromACard(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !strings.Contains(c.Req.Prompt, "This topic is about card") {
			return provider.Result{Text: "no card"}
		}
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which format?",
			"options": []any{map[string]any{"label": "CSV"}, map[string]any{"label": "PDF"}}}}})
		answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
		return provider.Result{Text: fmt.Sprint("The spec is in the card: ", answers["Which format?"])}
	}, func(c *app.Config) { c.QuestionWait = 2 * time.Minute })
	e.must("deck.project_create", map[string]any{"name": "work"}, nil)
	e.card(map[string]any{"project": "work", "title": "Export the report"})
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/work/board")
	b.until("the card", `[...document.querySelectorAll('[data-card]')].some((c) => c.innerText.includes('Export the report'))`)
	b.eval(`([...document.querySelectorAll('[data-card]')].find((c) => c.innerText.includes('Export the report')).querySelector('button').click(), '')`)
	b.until("the card editor", `document.querySelector('[data-verb="interview"]')`)
	b.type_(`document.querySelector('.mantine-Modal-body input')`, "Export the report as PDF")
	b.eval(`(document.querySelector('[data-verb="interview"]').click(), '')`)
	b.until("the chat with the question about the card", `document.querySelector('[data-chat-panel] [data-question]')?.innerText.includes('Which format?')`)
	b.eval(`([...document.querySelectorAll('[data-question] input')].find((i) => i.value === 'PDF').click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-question] button')].find((x) => !x.disabled && x.innerText.trim() !== '').click(), '')`)
	b.until("the reply", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('PDF'))`)
	var ts []chatTopic
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 1 || ts[0].Card == 0 {
		t.Fatalf("the interview is a topic about the card: %+v", ts)
	}
	var c cardV
	e.must("deck.card_get", map[string]any{"card": ts[0].Card}, &c)
	if c.Title != "Export the report as PDF" {
		t.Fatalf("the interview keeps the edit of the card it starts from: %+v", c)
	}
	b.shot("interview")
}

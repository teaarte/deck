package browser

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserMasterChat talks to the master from the side panel in the real browser: a topic and its reply,
// a question answered in the feed, a turn stopped, and the panel on the whole screen of a phone.
func TestRealBrowserMasterChat(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "which list"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "records_list", Text: "{}"})
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?",
				"options": []any{map[string]any{"label": "shop"}, map[string]any{"label": "market"}}}}})
			upd, _ := d["updatedInput"].(map[string]any)
			answers, _ := upd["answers"].(map[string]any)
			return provider.Result{Text: fmt.Sprint("Picked ", answers["Which list?"])}
		case strings.Contains(c.Req.Prompt, "post it") && !strings.Contains(c.Req.Prompt, "This continues"):
			out, _ := c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "milk"})
			return provider.Result{Text: "Asked: " + out}
		case strings.Contains(c.Req.Prompt, "think long"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Reading the list"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "records_list", Text: "{}"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Weighing the shops"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t3", Title: "Bash", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Put it into **groceries** as task #1. [Open](#/p/groceries/tasks)"}
	}), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 2 * time.Minute; c.ConfirmTTL = time.Hour }))

	e.Must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n", "manifest": harness.TasksManifest}, nil)
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "x"}`) })
	e.Secret("notes", "svc", "k")
	e.Project("notes", harness.PostManifest(svc.URL, ""))

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/p/groceries/tasks")
	b.until("the tasks", `document.querySelector('[data-block]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat beside the screen", `document.querySelector('[data-chat-panel] [data-chat-input]') && document.querySelector('[data-block]')`)
	b.until("an empty topic says what to do", `document.querySelector('[data-chat-empty]')?.innerText.includes('?')`)
	b.shot("chat-empty")
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	say("we are out of milk")
	b.until("the master's reply in the feed", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.querySelector('strong')?.innerText === 'groceries')`)
	b.shot("chat-reply")
	b.until("the screen the message goes with, by its titles", `document.querySelector('[data-context]')?.innerText.includes('groceries · Tasks')`)
	b.eval(`(document.querySelector('[data-context] button').click(), '')`)
	b.until("a message without the screen", `document.querySelector('[data-context-off]')`)
	b.eval(`(document.querySelector('[data-context-off]').click(), '')`)
	b.until("the screen back", `document.querySelector('[data-context]')`)
	ms := e.ChatMessages(1)
	if len(ms) != 2 || !strings.Contains(string(ms[0].Screen), `"project":"groceries"`) || !strings.Contains(string(ms[0].Screen), `"screen":"tasks"`) {
		t.Fatalf("the message carries the screen the owner is on: %+v", ms)
	}

	say("which list then?")
	b.until("the question in the feed", `document.querySelector('[data-question]')?.innerText.includes('Which list?')`)
	b.until("the turn with its calls folded", `document.querySelector('[data-turn] [data-turn-fold]') && !document.querySelector('[data-turn-log]')`)
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the calls under Log", `document.querySelector('[data-turn] [data-turn-line]')?.innerText.includes('records_list')`)
	b.eval(`(document.querySelector('[data-question] [data-option="market"]').click(), '')`)
	b.eval(`([...document.querySelectorAll('[data-question] button')].filter((x) => !x.disabled && x.innerText.trim() !== '').pop().click(), '')`)
	b.until("the reply after the answer", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('Picked market'))`)
	b.until("the question gone", `!document.querySelector('[data-question]')`)

	say("think long about it")
	b.until("the running turn says the call it makes in words with a timer and stop, its steps folded", `(() => { const t = document.querySelector('[data-turn="running"]'); return t && t.querySelector('[data-turn-now]')?.innerText === 'runs a command' &&
		/^\d+:\d\d$/.test(t.querySelector('[data-turn-timer]')?.innerText ?? '') && t.querySelector('[data-verb="stop"]') && !t.querySelector('[data-turn-step]') && !t.querySelector('[data-turn-log]') })()`)
	b.until("the head of the panel says it works", `document.querySelector('[data-chat-panel] [data-chat-live]')`)
	b.until("Deki in the turn in the pose of what it does", `document.querySelector('[data-turn="running"] [data-turn-head] [data-pose]')?.dataset.pose === 'work'`)
	b.shot("chat-turn")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the raw calls under Log", `document.querySelector('[data-turn-log] [data-turn-line]')?.innerText.includes('records_list') && document.querySelector('[data-turn-log] [data-verb="open"]')`)
	b.shot("chat-turn-log")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the log folded again", `!document.querySelector('[data-turn-log]')`)
	for _, w := range []int{1000, 800, 360, 300} {
		b.call("browser_resize", map[string]any{"width": w, "height": 800})
		b.until(fmt.Sprintf("the chat within its panel at %d", w), `(() => { const box = document.querySelector('[data-chat-panel]') ?? document.querySelector('[data-chat-phone] .mantine-Drawer-content'); if (!box) return false;
			const r = box.getBoundingClientRect(); return document.querySelector('[data-chat-send]') && ![...box.querySelectorAll('*')].some((x) => { const q = x.getBoundingClientRect(); return q.width > 0 && (q.right > r.right + 1 || q.left < r.left - 1) }) })()`)
		b.shot(fmt.Sprintf("chat-width-%d", w))
	}
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.until("the turn back in the side panel", `document.querySelector('[data-chat-panel] [data-turn] [data-verb="stop"]')`)
	b.eval(`(document.querySelector('[data-turn] [data-verb="stop"]').click(), '')`)
	b.until("the stopped reply says how far it got and goes on", `document.querySelector('[data-reply-cut][data-stopped] [data-verb="continue"]') && !document.querySelector('[data-turn]') && !document.querySelector('[data-chat-live]')`)
	b.shot("chat-stopped")

	say("post it to the notes")
	b.until("the yes it waits for, in the topic", `document.querySelector('[data-chat-ask] [data-verb="allow"]')?.innerText.includes('notes.post') || document.querySelector('[data-chat-ask] [data-verb="allow"]')?.innerText.includes('post')`)
	b.shot("chat-ask")
	b.eval(`(document.querySelector('[data-chat-ask] [data-verb="allow"]').click(), '')`)
	b.until("the card steps aside while the yes holds", `!document.querySelector('[data-chat-ask]') && document.querySelector('[data-toast]')`)
	b.until("the call made after the hold", `document.querySelector('[data-toast]') === null`)
	e.Until("the call made", func() bool { return len(svc.Requests()) == 1 })

	e.Must("deck.theme_save", map[string]any{"name": "square", "text": `{"version": 1, "title": {"en": "Square"}, "mood": "flat", "radius": "0"}`}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "square"}, nil)
	b.until("a theme of radius 0 squares the whole chat", `(() => { const p = document.querySelector('[data-chat-panel]'); if (!p || !document.querySelector('[data-bubble]') || !document.querySelector('[data-reply-cut]')) return false;
		const round = [p, ...p.querySelectorAll('*')].filter((x) => x.getBoundingClientRect().width > 0 && ['TopLeft', 'TopRight', 'BottomLeft', 'BottomRight'].some((c) => parseFloat(getComputedStyle(x)['border' + c + 'Radius']) > 0));
		return round.length === 0 })()`)
	b.shot("chat-square")
	e.Must("deck.theme_use", map[string]any{"name": "juce"}, nil)

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
	link := `([...document.querySelectorAll('[data-message="master"] a')].pop().click(), '')`
	b.eval(link)
	b.until("the screen of the link in place of the chat over it", `location.hash === '#/p/groceries/tasks' && !document.querySelector('[data-chat-phone] [data-chat-input]')?.offsetParent && document.querySelector('[data-block]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat over the screen again", `document.querySelector('[data-chat-phone] [data-message="master"] a')`)
	b.eval(link)
	b.until("a link to the screen under the chat shows that screen", `!document.querySelector('[data-chat-phone] [data-chat-input]')?.offsetParent`)
	var ts []harness.ChatTopic
	e.ChatTopics(false, &ts)
	if len(ts) != 2 || ts[0].Title != "from the phone" {
		t.Fatalf("the phone opened a topic of its own: %+v", ts)
	}
}

// TestRealBrowserVoice records the microphone in the chat of the real browser: the recording goes to the whisper
// server as WAV, and its text lands in the input, where the owner sends it.
func TestRealBrowserVoice(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
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
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "Put it into groceries."}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(() => { navigator.mediaDevices.getUserMedia = async () => { const ctx = new AudioContext(); const o = ctx.createOscillator();
		const dst = ctx.createMediaStreamDestination(); o.connect(dst); o.start(); return dst.stream; }; return '' })()`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the mic", `document.querySelector('[data-mic]')`)
	b.eval(`(document.querySelector('[data-mic]').click(), '')`)
	b.until("the recording in place of the field, nothing blinking", `document.querySelector('[data-mic-stop]') && document.querySelector('[data-hearing]') && !document.querySelector('[data-chat-input]') &&
		document.querySelector('[data-composer]').getAnimations({ subtree: true }).filter((a) => a.playState === 'running' && a.effect?.getTiming().iterations === Infinity).length === 0`)
	b.shot("chat-voice")
	time.Sleep(1500 * time.Millisecond)
	b.eval(`(document.querySelector('[data-mic-stop]').click(), '')`)
	b.until("the text in the input", `document.querySelector('[data-chat-input]').value === 'buy oat milk'`)
	if wav := harness.Receive(t, "the recording to reach whisper", heard); len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wav[24:28]) != 16000 {
		t.Fatalf("the recording is 16 kHz WAV: %d bytes %q", len(wav), wav[:min(len(wav), 12)])
	}
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the reply", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	b.shot("chat-voice")
}

// fakeMic gives the page a microphone that plays a tone.
const fakeMic = `(() => { navigator.mediaDevices.getUserMedia = async () => { const ctx = new AudioContext(); const o = ctx.createOscillator();
	const dst = ctx.createMediaStreamDestination(); o.connect(dst); o.start(); return dst.stream; }; return '' })()`

// voiceEnv is deck with whisper at a server that answers each recording with answer, and the chat open in the
// real browser with a microphone.
func voiceEnv(t *testing.T, answer func(n int, wav []byte) (int, string)) *playwright {
	var mu sync.Mutex
	n := 0
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(f)
		mu.Lock()
		n++
		code, body := answer(n, b)
		mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(whisper.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(fakeMic)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the mic", `document.querySelector('[data-mic]')`)
	return b
}

// TestRealBrowserVoiceOfMinutes: a recording of more than three minutes reaches whisper in pieces of at most a
// minute while it goes on, and after Stop its whole text lands in the input in order.
func TestRealBrowserVoiceOfMinutes(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	var seconds []float64
	b := voiceEnv(t, func(n int, wav []byte) (int, string) {
		seconds = append(seconds, float64(len(wav)-44)/32000)
		return 200, fmt.Sprintf(`{"text": "piece %d"}`, n)
	})
	b.eval(`(document.querySelector('[data-mic]').click(), '')`)
	b.until("the recording", `document.querySelector('[data-mic-stop]')`)
	time.Sleep(190 * time.Second)
	b.until("the recording going on past two minutes", `document.querySelector('[data-mic-stop]')?.innerText.includes('19')`)
	b.eval(`(document.querySelector('[data-mic-stop]').click(), '')`)
	b.until("the whole text in the input", `document.querySelector('[data-chat-input]').value === 'piece 1 piece 2 piece 3 piece 4'`)
	if len(seconds) != 4 || seconds[0] > 60.1 || seconds[0] < 44 || seconds[3] > 15 {
		t.Fatalf("the recording reached whisper in pieces of at most a minute: %v", seconds)
	}
}

// TestRealBrowserVoiceKeptOnFailure: a recording whisper did not turn into text stays with why, and Recognize again
// sends the same recording once more, whose text lands in the input.
func TestRealBrowserVoiceKeptOnFailure(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	var got [][]byte
	b := voiceEnv(t, func(n int, wav []byte) (int, string) {
		got = append(got, wav)
		if n == 1 {
			return 503, `{"error": "whisper is loading"}`
		}
		return 200, `{"text": "call the plumber"}`
	})
	b.eval(`(document.querySelector('[data-mic]').click(), '')`)
	b.until("the recording", `document.querySelector('[data-mic-stop]')`)
	time.Sleep(1500 * time.Millisecond)
	b.eval(`(document.querySelector('[data-mic-stop]').click(), '')`)
	b.until("the recording kept with why", `document.querySelector('[data-voice-failed]')?.innerText.includes('503')`)
	b.eval(`(document.querySelector('[data-voice-again]').click(), '')`)
	b.until("the text in the input", `document.querySelector('[data-chat-input]').value === 'call the plumber' && !document.querySelector('[data-voice-failed]')`)
	if len(got) != 2 || !bytes.Equal(got[0], got[1]) {
		t.Fatalf("the same recording went again: %d", len(got))
	}
}

// TestRealBrowserVoiceAfterAFailedOne: a second tap while the microphone starts records once; a recording made while
// a failed one waits lands in the input, and Discard drops only the failed one.
func TestRealBrowserVoiceAfterAFailedOne(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	b := voiceEnv(t, func(n int, wav []byte) (int, string) {
		if n == 1 {
			return 503, `{"error": "whisper is loading"}`
		}
		return 200, `{"text": "second thought"}`
	})
	record := func() {
		b.eval(`(document.querySelector('[data-mic]').click(), document.querySelector('[data-mic]')?.click(), '')`)
		b.until("the recording", `document.querySelector('[data-mic-stop]')`)
		time.Sleep(1500 * time.Millisecond)
		b.eval(`(document.querySelector('[data-mic-stop]').click(), '')`)
	}
	record()
	b.until("the recording kept with why", `document.querySelector('[data-voice-failed]')`)
	record()
	b.until("the second recording in the input, the first still kept", `document.querySelector('[data-chat-input]').value === 'second thought' && document.querySelector('[data-voice-failed]') && !document.querySelector('[data-mic-stop]')`)
	b.eval(`(document.querySelector('[data-voice-discard]').click(), '')`)
	b.until("the failed one dropped, the text kept", `!document.querySelector('[data-voice-failed]') && document.querySelector('[data-chat-input]').value === 'second thought'`)
	time.Sleep(1500 * time.Millisecond)
	if v := b.eval(`document.querySelector('[data-mic-stop]') ? 'recording' : 'quiet'`); !strings.Contains(v, "quiet") {
		t.Fatalf("the second tap started no recording of its own: %s", v)
	}
}

// TestRealBrowserVoiceWithoutAMicrophone: a page the browser gives no microphone, such as deck on http over the
// network, says why and takes a recording as a file, which reaches whisper as 16 kHz WAV.
func TestRealBrowserVoiceWithoutAMicrophone(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
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
		_, _ = w.Write([]byte(`{"text": "call the plumber"}`))
	}))
	t.Cleanup(whisper.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(Object.defineProperty(navigator, 'mediaDevices', { value: undefined, configurable: true }), '')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the mic", `document.querySelector('[data-mic]')`)
	b.eval(`(document.querySelector('[data-mic]').click(), '')`)
	b.until("why there is no microphone", `document.querySelector('[data-no-mic]') && document.body.innerText.includes('https')`)
	b.eval(`(() => {
		const rate = 44100, n = rate, buf = new ArrayBuffer(44 + n * 2), v = new DataView(buf);
		const text = (at, s) => [...s].forEach((c, i) => v.setUint8(at + i, c.charCodeAt(0)));
		text(0, 'RIFF'); v.setUint32(4, 36 + n * 2, true); text(8, 'WAVE'); text(12, 'fmt '); v.setUint32(16, 16, true);
		v.setUint16(20, 1, true); v.setUint16(22, 1, true); v.setUint32(24, rate, true); v.setUint32(28, rate * 2, true);
		v.setUint16(32, 2, true); v.setUint16(34, 16, true); text(36, 'data'); v.setUint32(40, n * 2, true);
		for (let i = 0; i < n; i++) v.setInt16(44 + i * 2, Math.sin(i / 20) * 8000, true);
		const input = document.querySelector('[data-voice-input]'), dt = new DataTransfer();
		dt.items.add(new File([buf], 'memo.wav', { type: 'audio/wav' }));
		input.files = dt.files;
		input.dispatchEvent(new Event('change', { bubbles: true }));
		return '';
	})()`)
	b.until("the text in the input", `document.querySelector('[data-chat-input]').value === 'call the plumber'`)
	if wav := harness.Receive(t, "the recording to reach whisper", heard); len(wav) < 44 || string(wav[:4]) != "RIFF" || binary.LittleEndian.Uint32(wav[24:28]) != 16000 {
		t.Fatalf("the file reaches whisper as 16 kHz WAV: %d bytes", len(wav))
	}
}

// TestRealBrowserChatDeletesAWaitingMessage: a message the master has not taken has a delete button, which takes
// it away; the message its waiting reply took has none.
func TestRealBrowserChatDeletesAWaitingMessage(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Text: "Hi."}
	}))

	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	e.WaitState(e.Run(e.Project("busy", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work"), "running")

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-block]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-panel] [data-chat-input]')`)
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	say("first")
	b.until("the first message, taken by the reply that waits for a slot", `document.querySelectorAll('[data-message="owner"]').length === 1 && !document.querySelector('[data-message="owner"] [data-verb="delete"]')`)
	say("sent by mistake")
	b.until("the second message with its delete button", `document.querySelectorAll('[data-message="owner"]').length === 2 && document.querySelectorAll('[data-message="owner"] [data-verb="delete"]').length === 1`)
	b.shot("chat-delete")
	b.eval(`(document.querySelector('[data-message="owner"] [data-verb="delete"]').click(), '')`)
	b.until("the deleted message gone", `document.querySelectorAll('[data-message="owner"]').length === 1 && !document.querySelector('[data-message="owner"] [data-verb="delete"]')`)
	var topics []struct{ ID int64 }
	e.ChatTopics(false, &topics)
	if ms := e.ChatMessages(topics[0].ID); len(ms) != 1 || ms[0].Text != "first" {
		t.Fatalf("only the message the master took stays: %+v", ms)
	}
	b.until("what the reply waits for, in the language of the interface", `document.querySelector('[data-turn="waiting_slot"]')?.innerText.includes('waits for a slot')`)
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	b.until("what the reply waits for, in Russian", `(() => { const x = document.querySelector('[data-turn="waiting_slot"]')?.innerText ?? ''; return x.includes('свободного места') && !x.includes('slot') })()`)
}

// TestRealBrowserChatInput: pasted and dropped files attach as the plus does, the field has no focus ring, the turn is
// one line that opens in place, and a message tells its model, cost and time on hover or a press.
func TestRealBrowserChatInput(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "think long") {
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Reading the list"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t2", Title: "records_list", Text: "{}"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Weighing the shops"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t3", Title: "Bash", Text: "{}"})
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Put it into groceries."}
	}))

	e.Must("deck.theme_save", map[string]any{"name": "ringed", "text": `{"version": 1, "title": {"en": "Ringed"}, "mood": "flat", "radius": "md",
		"css": ".field:focus { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 22%, transparent); }"}`}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "ringed"}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-input]')`)

	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('[data-chat-input]').click(); await page.keyboard.type('a'); }`})
	b.until("the field in focus without a ring", `(() => { const i = document.querySelector('[data-chat-input]'); const st = getComputedStyle(i);
		return document.activeElement === i && st.boxShadow === 'none' && st.outlineStyle === 'none' })()`)
	b.shot("input-focus-pc")
	b.eval(pasteFiles)
	b.until("the pasted picture and file over the field", `document.querySelector('[data-attached-picture] img')?.naturalWidth === 16 &&
		document.querySelector('[data-attached-file]')?.textContent.includes('list.txt') && document.querySelector('[data-chat-input]').value === 'a'`)
	b.eval(`(() => { const dt = new DataTransfer(); dt.setData('text/html', '<table><tr><td>1</td></tr></table>'); dt.setData('text/plain', '1');
		dt.items.add(new File(['x'], 'cells.png', { type: 'image/png' }));
		document.querySelector('[data-chat-input]').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); return '' })()`)
	b.eval(`(() => { const dt = new DataTransfer(); dt.items.add(new File(['eggs'], 'dropped.txt', { type: 'text/plain' }));
		document.querySelector('[data-composer]').dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true })); return '' })()`)
	b.until("the dropped file beside them, a copy of cells from an app kept as its text", `[...document.querySelectorAll('[data-attached-file]')].some((f) => f.textContent.includes('dropped.txt')) &&
		document.querySelectorAll('[data-attached] > *').length === 3`)
	b.shot("input-paste-pc")
	b.type_(`document.querySelector('[data-chat-input]')`, "we are out of milk")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the reply", `[...document.querySelectorAll('[data-message="master"]')].some((m) => m.innerText.includes('groceries'))`)
	ms := e.ChatMessages(1)
	var names []string
	for _, f := range ms[0].Files {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"dropped.txt", "image.png", "list.txt"}) {
		t.Fatalf("the pasted and dropped files went with the message: %v", names)
	}

	hover := func(sel string) {
		b.call("browser_run_code_unsafe", map[string]any{"code": fmt.Sprintf(`async (page) => { await page.locator(%q).last().hover(); }`, sel)})
	}
	tools := func(sel string) string {
		return fmt.Sprintf(`[...document.querySelectorAll(%q)].pop()?.querySelector(':scope > [data-line-tools]')`, sel)
	}
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.mouse.move(5, 890); }`})
	b.until("how a message went hidden without hover", `getComputedStyle(`+tools(`[data-message="master"]`)+`).opacity === '0'`)
	hover(`[data-message="master"]`)
	b.until("the model, the time it took and when, on hover", `(() => { const m = `+tools(`[data-message="master"]`)+`; return getComputedStyle(m).opacity === '1' &&
		/^fake · .* · \d{1,2}:\d\d/.test(m.querySelector('[data-work-meta]').innerText) })()`)
	b.shot("input-hover-pc")
	hover(`[data-message="owner"]`)
	b.until("when the owner wrote it, on hover", `(() => { const m = `+tools(`[data-message="owner"]`)+`; return getComputedStyle(m).opacity === '1' &&
		/^\d{1,2}:\d\d/.test(m.innerText) })()`)
	b.shot("input-hover-owner-pc")

	b.type_(`document.querySelector('[data-chat-input]')`, "think long about it")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the turn in one line: what it does, a timer, the model and Stop, its steps folded", oneLineTurn)
	b.shot("input-turn-pc")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the steps and the calls under the line, as wide as it and not moved right", openedTurn)
	b.shot("input-turn-open-pc")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the turn folded again", `!document.querySelector('[data-turn-open]') && document.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'false'`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the chat on the phone", `document.querySelector('[data-chat-input]')?.getBoundingClientRect().width > 300 && document.querySelector('[data-turn="running"]')`)
	b.until("the turn in one line on the phone", oneLineTurn)
	b.shot("input-turn-phone")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.until("the steps and the calls on the phone, not moved right", openedTurn)
	b.shot("input-turn-open-phone")
	b.eval(`(document.querySelector('[data-turn-fold]').click(), '')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.mouse.move(5, 5); }`})
	b.eval(`([...document.querySelectorAll('[data-message="owner"] [data-bubble]')][0].click(), '')`)
	b.until("on a phone a press shows when the message was written", `(() => { const m = document.querySelector('[data-message="owner"]');
		return m.dataset.shown !== undefined && getComputedStyle(m.querySelector(':scope > [data-line-tools]')).opacity === '1' })()`)
	b.shot("input-press-phone")
}

// pasteFiles pastes a picture and a text file into the chat input as a paste from the clipboard does.
const pasteFiles = `(() => { const i = document.querySelector('[data-chat-input]'); const c = document.createElement('canvas'); c.width = c.height = 16;
	const g = c.getContext('2d'); g.fillStyle = '#c33'; g.fillRect(0, 0, 16, 16);
	const bin = atob(c.toDataURL('image/png').split(',')[1]); const bytes = Uint8Array.from(bin, (x) => x.charCodeAt(0));
	const dt = new DataTransfer(); dt.items.add(new File([bytes], 'image.png', { type: 'image/png' })); dt.items.add(new File(['milk, oats'], 'list.txt', { type: 'text/plain' }));
	i.focus(); i.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); return '' })()`

// oneLineTurn holds when the running turn is one line of Deki, what it does, the timer, the model, the fold and
// Stop, with nothing under it.
const oneLineTurn = `(() => { const t = document.querySelector('[data-turn="running"]'); const h = t?.querySelector('[data-turn-head]'); if (!h) return false;
	const r = h.getBoundingClientRect(); const parts = ['[data-pose]', '[data-turn-now]', '[data-turn-timer]', '[data-turn-model]', '[data-verb="stop"]'].map((q) => h.querySelector(q));
	return parts.every((p) => p && p.getBoundingClientRect().width > 0 && p.getBoundingClientRect().top >= r.top - 1 && p.getBoundingClientRect().bottom <= r.bottom + 1) &&
		r.height < 44 && h.querySelector('[data-turn-now]').innerText === 'runs a command' && h.querySelector('[data-turn-model]').innerText === 'fake' &&
		h.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'false' && !t.querySelector('[data-turn-step]') && !t.querySelector('[data-turn-open]') &&
		t.getBoundingClientRect().height < r.height + 2 })()`

// openedTurn holds when the turn shows its two steps and its calls under its line, starting at most a few pixels
// right of the line and ending within it.
const openedTurn = `(() => { const t = document.querySelector('[data-turn="running"]'); const h = t?.querySelector('[data-turn-head]'); const body = t?.querySelector('[data-turn-open]');
	if (!body) return false; const r = h.getBoundingClientRect();
	const inner = [...body.querySelectorAll('[data-turn-step], [data-turn-line]')];
	return h.querySelector('[data-turn-fold]').getAttribute('aria-expanded') === 'true' && body.querySelectorAll('[data-turn-step]').length === 2 && inner.some((l) => l.innerText.includes('records_list')) &&
		[body, ...body.querySelectorAll('*')].every((x) => { const q = x.getBoundingClientRect(); return q.width === 0 || (q.left >= r.left - 1 && q.right <= r.right + 1) }) &&
		inner.every((l) => l.getBoundingClientRect().left - r.left <= 20) })()`

// TestRealBrowserChatStaysOnTheLastReply: the feed of the master opens, reloads and grows at its last reply:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios/browser -run TestRealBrowserChatStaysOnTheLastReply -v.
func TestRealBrowserChatStaysOnTheLastReply(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "## Done\n\n" + strings.Repeat("A long line of the reply that takes room in the feed. ", 12)}
	}))

	e.Must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list.\n"}, nil)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 800})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat", `document.querySelector('[data-chat-input]')`)
	for i := range 6 {
		b.type_(`document.querySelector('[data-chat-input]')`, "message "+string(rune('a'+i)))
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
		b.until("the reply", `document.querySelectorAll('[data-message="master"]').length === `+string(rune('1'+i)))
	}
	bottom := `(() => { const v = document.querySelector('[data-feed] .mantine-ScrollArea-viewport') || document.querySelector('[data-feed]');
		return v.scrollHeight > v.clientHeight + 100 && v.scrollHeight - v.scrollTop - v.clientHeight < 8 })()`
	b.until("the feed at its last reply", bottom)
	e.Must("deck.project_create", map[string]any{"name": "other", "prompt": "# Other.\n"}, nil)
	b.until("the feed at its last reply after an event", bottom)
	b.open(e.URL + "/#/p/groceries/board")
	b.until("the feed at its last reply after a move", bottom)
	b.eval(`(location.reload(), '')`)
	b.until("the chat after a reload", `document.querySelectorAll('[data-message="master"]').length === 6`)
	b.until("the feed at its last reply after a reload", bottom)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const box = await page.locator('[data-feed]').boundingBox();
		await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2); await page.mouse.wheel(0, -5000); }`})
	b.until("the owner up in the feed", `document.querySelector('[data-feed] .mantine-ScrollArea-viewport').scrollTop < 100`)
	b.type_(`document.querySelector('[data-chat-input]')`, "one more")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the feed at the reply to a message the owner sent from above", `document.querySelectorAll('[data-message="master"]').length === 7 && `+bottom)
}

// TestRealBrowserChatTopicsFollowTheModuleFilter: the module picked above the chat narrows its topics, without a
// module too: DECK_REAL_BROWSER=1 go test ./tests/scenarios/browser -run TestRealBrowserChatTopicsFollowTheModuleFilter -v.
func TestRealBrowserChatTopicsFollowTheModuleFilter(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "ok"} }))
	e.Project("web", harness.WebNotes)
	e.MasterSaid(e.Say(0, "about web", map[string]any{"screen": `{"project":"web","screen":"main"}`}), 1)
	e.MasterSaid(e.Say(0, "about master", map[string]any{"screen": `{"project":"master","screen":"documents"}`}), 1)
	e.MasterSaid(e.Say(0, "about nothing"), 1)

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the module filter", `document.querySelector('[data-topic-project]')`)
	topics := func() string {
		return `(() => { document.querySelector('[data-topics]').click(); const id = document.querySelector('[data-topics]').getAttribute('aria-controls');
			const box = id && document.getElementById(id); return [...(box || document).querySelectorAll('[role=option]')].map((o) => o.innerText.trim()).sort().join('|') })()`
	}
	b.until("all topics", topics()+` === 'about master|about nothing|about web'`)
	b.eval(`(document.body.click(), '')`)
	scope := func(label string) {
		b.eval(`(document.querySelector('[data-topic-project]').click(), '')`)
		b.until("the module "+label, `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].some((x) => x.innerText.trim() === '`+label+`')`)
		b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === '` + label + `').click(), '')`)
	}
	scope("web")
	b.until("the last topic of web open", `document.querySelector('[data-topics]').value === 'about web'`)
	b.until("only the topics of web", topics()+` === 'about web'`)
	b.eval(`(document.body.click(), '')`)
	scope("Without a module")
	b.until("only the topics without a module", topics()+` === 'about nothing'`)
	b.eval(`(document.querySelector('[data-topic-project]').click(), '')`)
	b.until("the modules opened last on top", `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].map((x) => x.innerText.trim()).slice(1, 3).join('|') === 'Without a module|web'`)
	b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === 'All modules').click(), '')`)
	options := `(() => { document.querySelector('[data-topics]').click(); const id = document.querySelector('[data-topics]').getAttribute('aria-controls');
		const box = id && document.getElementById(id); return [...(box || document).querySelectorAll('[role=option]')].map((o) => o.innerText.trim()).join('|') })()`
	b.until("the topics newest first", options+` === 'about nothing|about master|about web'`)
	b.eval(`([...document.querySelectorAll('[role=option]')].find((o) => o.innerText.trim() === 'about web').click(), '')`)
	b.until("the topic picked", `document.querySelector('[data-topics]').value === 'about web'`)
	b.until("the open topic first", options+` === 'about web|about nothing|about master'`)
	b.eval(`(document.body.click(), '')`)

	b.until("the model of the topic, the master's own", `document.querySelector('[data-topic-model]')?.dataset.topicModel === 'fake' && document.querySelector('[data-topic-model]').dataset.own === undefined`)
	b.eval(`(document.querySelector('[data-topic-model]').click(), '')`)
	b.until("the models to pick, the master's own once", `document.querySelector('[data-topic-models] [data-model="quiet"]') && !document.querySelector('[data-topic-models] [data-model="fake"]')`)
	b.eval(`(document.querySelector('[data-topic-models] [data-model="quiet"]').click(), '')`)
	b.until("the topic keeps the model picked", `document.querySelector('[data-topic-model]').dataset.own !== undefined`)

	b.type_(`document.querySelector('[data-chat-input]')`, "remember this line")
	b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	b.until("the input empty after sending", `document.querySelector('[data-chat-input]').value === ''`)
	key := func(k string) {
		b.eval(`(document.querySelector('[data-chat-input]').dispatchEvent(new KeyboardEvent('keydown', { key: '` + k + `', bubbles: true })), '')`)
	}
	key("ArrowUp")
	b.until("arrow up brings back the message sent", `document.querySelector('[data-chat-input]').value === 'remember this line'`)
	key("ArrowDown")
	b.until("arrow down goes back to an empty input", `document.querySelector('[data-chat-input]').value === ''`)
}

// TestRealBrowserAWorkToldInItsTopic: a work of the master that failed says so in its topic with a button that goes
// on; gone on, its long result comes whole, folded with a button that shows all, on a computer and a phone.
func TestRealBrowserAWorkToldInItsTopic(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	long := strings.Repeat("Шаг сделан, одна фраза — одно дело. ", 90) + "Что заводить первым?"
	var from atomic.Int64
	var works atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "go on where you stopped"):
			return provider.Result{Text: long}
		case c.Command() != "work":
			return harness.HandOver(ctx, c, &from)
		case works.Add(1) == 1:
			return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
		}
		return provider.Result{Text: "a second fresh run"}
	}))

	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Project("plain", harness.PlainManifest)
	topic := e.Say(0, "сделай")
	e.Told(topic, "error", "ru")
	b := Open(t)
	open := func(phone bool) {
		if phone {
			b.call("browser_resize", map[string]any{"width": 390, "height": 844})
		} else {
			b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
		}
		b.Open(e, "#/deck/cockpit")
		b.until("the cockpit or its chat", `document.querySelector('[data-chat-toggle], [data-told]')`)
		time.Sleep(500 * time.Millisecond)
		b.eval(`(document.querySelector('[data-told]')?.offsetParent || document.querySelector('[data-chat-toggle]').click(), '')`)
	}
	for _, phone := range []bool{false, true} {
		open(phone)
		b.until("the failed work with its button that goes on", `[...document.querySelectorAll('[data-told="error"] button')].some((x) => x.innerText.trim() === 'Продолжить')`)
		b.shot("told-failed-" + map[bool]string{false: "computer", true: "phone"}[phone])
	}
	b.eval(`([...document.querySelectorAll('[data-told="error"] button')].find((x) => x.innerText.trim() === 'Продолжить').click(), '')`)
	for _, phone := range []bool{true, false} {
		if !phone {
			open(phone)
		}
		look := map[bool]string{false: "computer", true: "phone"}[phone]
		b.until("the whole result, folded", `document.querySelector('[data-told="done"] [data-told-result]')?.innerText.includes('Что заводить первым?')`)
		if got := b.eval(`(() => { const r = document.querySelector('[data-told="done"] [data-told-result]'); return r.getBoundingClientRect().height < 480 && [...r.querySelectorAll('button')].some((x) => x.innerText.trim() === 'Показать всё') ? 'folded' : r.innerText.slice(-200); })()`); got != "folded" {
			t.Fatalf("a long result on a %s is folded with a button that shows all: %s", look, got)
		}
		b.shot("told-folded-" + look)
		b.eval(`([...document.querySelectorAll('[data-told="done"] [data-told-result] button')].find((x) => x.innerText.trim() === 'Показать всё').click(), '')`)
		b.until("the whole result shown", `document.querySelector('[data-told="done"] [data-told-result]').getBoundingClientRect().height > 600`)
		b.shot("told-whole-" + look)
		if !phone {
			break
		}
	}
	if works.Load() != 1 {
		t.Fatalf("the work went on in its session, not afresh: %d fresh runs", works.Load())
	}
}

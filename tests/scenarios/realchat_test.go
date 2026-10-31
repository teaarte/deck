package scenarios

import (
	"bytes"
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRealBrowserMasterChat talks to the master from the side panel in the real browser: a topic and its reply,
// a question answered in the feed, a turn stopped, and the panel on the whole screen of a phone.
func TestRealBrowserMasterChat(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "which list"):
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "records_list", Text: "{}"})
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?",
				"options": []any{map[string]any{"label": "shop"}, map[string]any{"label": "market"}}}}})
			answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
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
	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	e.Project("notes", postManifest(svc.URL, ""))

	b := newPlaywright(t)
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
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "Put it into groceries."}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	b := newPlaywright(t)
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
	b := newPlaywright(t)
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
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
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
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
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
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
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
		_, _ = w.Write([]byte(`{"text": "call the plumber"}`))
	}))
	t.Cleanup(whisper.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	b := newPlaywright(t)
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
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
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

	b := newPlaywright(t)
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

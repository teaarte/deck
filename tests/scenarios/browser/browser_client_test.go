package browser

import (
	"bytes"
	"context"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRealBrowserWebClient builds the web client and drives it in the real browser of Playwright MCP:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios/browser -run TestRealBrowser -v (DECK_SCREENSHOTS=<dir> keeps screenshots).
func TestRealBrowserWebClient(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")

	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithClock(clock), harness.WithConfig(harness.WithTTL))
	p := e.Project("notes", harness.WebManifest(svc.URL))
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": harness.Jsonl(map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 7})}, nil)
	e.WaitState(e.Run(p, "work"), "waiting_answer", "done")
	e.App.Sweep(context.Background(), clock.Add(24*time.Hour+time.Minute))
	e.Run(p, "work")
	e.Until("the fresh confirmation", func() bool { return len(e.CockpitItems()) == 2 })
	asking := e.Run(p, "ask")
	e.Until("the question", func() bool { return len(e.CockpitItems()) == 3 })

	front := newFront(t, e.URL)
	b := Open(t)
	b.OpenVia(front.URL, e, "#/deck/cockpit")
	card := `document.querySelector('[data-focus-card]')`
	buttons := `[...` + card + `.querySelectorAll('button')].map(x => x.innerText.trim())`
	press := func(label string) string {
		return `([...` + card + `.querySelectorAll('button')].find(x => x.innerText.trim() === '` + label + `').click(), '')`
	}
	b.until("the decisions one at a time, three of them", `document.querySelector('[data-focus]')?.dataset.left === '3' && document.querySelector('[data-of]')?.innerText === '1 of 3'`)
	b.shot("cockpit")
	if got := b.eval(`JSON.stringify(` + buttons + `)`); got != `["Details","Deny","Later"]` {
		t.Fatalf("an expired confirmation shows only deny: %s", got)
	}
	if got := b.eval(`document.querySelector('[data-unknown="hologram"]')?.innerText ?? ''`); !strings.Contains(got, "Open in web") {
		t.Fatalf("a block the client does not know is drawn as open in web: %q", got)
	}
	b.eval(`(document.querySelector('[data-later]').click(), '')`)
	b.until("Later shows the next one", `document.querySelector('[data-kind]')?.innerText === 'Action' && `+card+`.innerText.includes('POST')`)
	if got := b.eval(`JSON.stringify(` + buttons + `)`); got != `["Details","Allow: post","Deny","Deny because…","Later"]` {
		t.Fatalf("a fresh confirmation says what its yes allows first: %s", got)
	}
	if got := b.eval(card + `.innerText`); !strings.Contains(got, "post") || !strings.Contains(got, "POST "+svc.URL+"/items/n2") || !strings.Contains(got, "notes") || strings.Contains(got, "notes.post") {
		t.Fatalf("a confirmation says what by its title, where and its module, the command under its details: %q", got)
	}
	b.eval(press("Details"))
	b.until("the frozen call", card+`.querySelector('pre')?.innerText.includes('"id": "n2"')`)
	b.eval(press("Deny because…"))
	b.until("the reason field", card+`.querySelector('input')`)
	b.eval(`(() => { const i = ` + card + `.querySelector('input');
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(i, 'keep me');
		i.dispatchEvent(new Event('input', { bubbles: true })); return '' })()`)
	b.eval(press("Send"))
	b.until("the toast that offers to take the answer back", `document.querySelector('[data-toast]')?.innerText.includes('Deny because')`)
	b.eval(`(document.querySelector('[data-undo-button]').click(), '')`)
	b.until("the decision back after Undo", `document.querySelector('[data-of]')?.innerText === '1 of 3' && `+card+`.innerText.includes('POST')`)
	if n := front.answers.Load(); n != 0 {
		t.Fatalf("an answer taken back is never sent: %d", n)
	}

	full := front.full.Load()
	b.eval(`(() => { const x = [...` + card + `.querySelectorAll('button')].find(x => x.innerText === 'Allow: post'); x.click(); x.click(); return '' })()`)
	b.until("the allowed one steps aside at once", `document.querySelector('[data-of]')?.innerText === '1 of 2'`)
	b.shot("cockpit-hold")
	e.Until("the answer once its moment passed", func() bool { return front.answers.Load() == 1 })
	b.until("the allowed one gone", `document.querySelector('[data-focus]')?.dataset.left === '2'`)
	if n, calls := front.answers.Load(), len(svc.Requests()); n != 1 || calls != 1 {
		t.Fatalf("a second press while the call waits sends nothing: %d answers, %d calls", n, calls)
	}
	if front.topics.Load() == 0 || front.full.Load() != full {
		t.Fatalf("after an event only the blocks of its topic are read again: %d topic reads, %d full", front.topics.Load(), front.full.Load()-full)
	}

	b.until("the question, the expired one put last", `document.querySelector('[data-kind]')?.innerText === 'Question'`)
	b.eval(`(` + card + `.querySelector('[data-option="go"]').click(), '')`)
	b.until("answer to unlock", `!([...`+card+`.querySelectorAll('button')].find(x => x.innerText === 'Answer')?.disabled ?? true)`)
	b.eval(press("Answer"))
	b.until("the expired one last", `document.querySelector('[data-of]')?.innerText === '1 of 1' && `+card+`.innerText.includes('Deny')`)
	b.eval(press("Deny"))
	b.until("all decided, Deki glad", `document.querySelector('[data-focus-done] [data-deki="done"]')`)
	e.WaitState(asking, "done")
	e.Until("every answer sent", func() bool { return len(e.CockpitItems()) == 0 })

	front.slow.Store(true)
	late := e.Run(p, "work")
	e.Until("the delayed read", func() bool { return front.delayed.Load() })
	for _, it := range e.CockpitItems() {
		e.Must("deck.ask_answer", map[string]any{"ask": it.ID, "answer": "deny"}, nil)
	}
	e.WaitState(late, "done", "error")
	time.Sleep(3 * time.Second)
	if got := b.eval(`String(!!document.querySelector('[data-focus-done]') && !document.querySelector('[data-focus-card]'))`); got != "true" {
		t.Fatalf("a late read does not bring back what a newer one cleared: %s", got)
	}

	b.open(front.URL + "/#/p/notes/main")
	b.until("the project screen with the title of its tabs", `document.body.innerText.includes('All notes') && document.body.innerText.includes('Views')`)
	b.shot("project")
	if got := b.eval(`document.querySelector('[data-block]').innerText`); !strings.Contains(got, "2") {
		t.Fatalf("the stat counts the notes: %q", got)
	}
	b.eval(`[...document.querySelectorAll('button')].find(x => x.innerText === 'Open').click(), ''`)
	b.until("the record page", `location.hash.startsWith('#/p/notes/note/') && document.body.innerText.includes('Note: beta')`)
	b.shot("record")
	b.eval(`[...document.querySelectorAll('button')].find(x => x.innerText === 'Touch').click(), ''`)
	b.until("the refusal text of the server", `document.body.innerText.includes('deck: refused by the test')`)

	before := front.full.Load()
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	e.Until("a full read on return", func() bool { return front.full.Load() > before })
	front.cut()
	b.until("the stale mark while the live connection is down", `document.querySelector('[data-stale]')`)
	e.Until("a refused reconnect", func() bool { return front.refused.Load() > 0 })
	before = front.full.Load()
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	e.Until("a full read while the live connection is down", func() bool { return front.full.Load() > before })
	time.Sleep(300 * time.Millisecond)
	if b.eval(`String(!!document.querySelector('[data-stale]'))`) != "true" {
		t.Fatal("a read while the live connection is down leaves the stale mark")
	}
	before = front.full.Load()
	front.heal()
	b.until("the stale mark to clear", `!document.querySelector('[data-stale]')`)
	if front.full.Load() <= before {
		t.Fatalf("a restored live connection reads the whole screen: %d", front.full.Load())
	}

	b.open(front.URL + "/#/deck/legacy")
	b.until("the refusal of an unknown protocol", `document.querySelector('[data-screen-error]')?.innerText.includes('speaks A2UI v0.8')`)
}

// pastUntil moves the end of every button that allows always into the past, as if its confirmation ran out on screen.
func pastUntil(v any) {
	switch x := v.(type) {
	case map[string]any:
		if p, ok := x["params"].(map[string]any); ok && p["answer"] == "allow_always" {
			x["until"] = "2000-01-01T00:00:00Z"
		}
		for _, y := range x {
			pastUntil(y)
		}
	case []any:
		for _, y := range x {
			pastUntil(y)
		}
	}
}

// front stands before deck: it counts screen reads and plays a newer server for the cases the client must survive.
type frontServer struct {
	*httptest.Server
	full, topics, answers, refused atomic.Int32
	// streams counts the live connections open now; fail refuses every screen read.
	streams atomic.Int32
	// slow delays the next asks read by two seconds after deck answered it.
	slow, delayed, fail atomic.Bool
	mu                  sync.Mutex
	down                bool
	cancels             []context.CancelFunc
}

// cut drops the live connections and refuses new ones until heal.
func (f *frontServer) cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = true
	for _, c := range f.cancels {
		c()
	}
	f.cancels = nil
}

func (f *frontServer) heal() {
	f.mu.Lock()
	f.down = false
	f.mu.Unlock()
}

func newFront(t *testing.T, deck string) *frontServer {
	t.Helper()
	target, _ := url.Parse(deck)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	f := &frontServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/commands/notes.touch":
			http.Error(w, "deck: refused by the test", http.StatusConflict)
			return
		case "/api/commands/deck.screen":
			if f.fail.Load() {
				http.Error(w, "deck: the read failed in the test", http.StatusInternalServerError)
				return
			}
		case "/api/commands/deck.ask_answer":
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"reason":"keep me"`)) {
				http.Error(w, "deck: refused by the test", http.StatusConflict)
				return
			}
			f.answers.Add(1)
			r.Body = io.NopCloser(bytes.NewReader(body))
			proxy.ServeHTTP(w, r)
			return
		case "/api/events":
			f.mu.Lock()
			if f.down {
				f.refused.Add(1)
				f.mu.Unlock()
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			ctx, cancel := context.WithCancel(r.Context())
			f.cancels = append(f.cancels, cancel)
			f.mu.Unlock()
			f.streams.Add(1)
			defer f.streams.Add(-1)
			proxy.ServeHTTP(w, r.WithContext(ctx))
			return
		default:
			proxy.ServeHTTP(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var in struct{ Screen, Topic string }
		_ = json.Unmarshal(body, &in)
		hold := in.Topic == "asks" && f.slow.CompareAndSwap(true, false)
		if in.Topic != "" {
			f.topics.Add(1)
		} else {
			f.full.Add(1)
		}
		legacy := in.Screen == "legacy"
		if legacy {
			body = []byte(`{"screen": "cockpit"}`)
		}
		req, _ := http.NewRequest("POST", deck+r.URL.Path, bytes.NewReader(body))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		var msgs []map[string]any
		if resp.StatusCode == 200 && json.Unmarshal(out, &msgs) == nil {
			for _, m := range msgs {
				if legacy {
					m["version"] = "v0.8"
				}
				if dm, ok := m["updateDataModel"].(map[string]any); ok && in.Screen == "cockpit" {
					pastUntil(dm["value"])
				}
				uc, ok := m["updateComponents"].(map[string]any)
				if !ok || in.Screen != "cockpit" {
					continue
				}
				comps := uc["components"].([]any)
				root := comps[0].(map[string]any)
				root["children"] = append(root["children"].([]any), "alien")
				uc["components"] = append(comps, map[string]any{"id": "alien", "component": "hologram", "depth": 3})
			}
			out, _ = json.Marshal(msgs)
		}
		if hold {
			f.delayed.Store(true)
			time.Sleep(2 * time.Second)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	}))
	t.Cleanup(f.Close)
	return f
}

// TestRealBrowserClientKeepsWhatItShows: a failed read leaves the screen, the live channel stays one, a screen change
// draws no old screen, a document save does not overwrite a newer revision: DECK_REAL_BROWSER=1 go test -run TestRealBrowser
func TestRealBrowserClientKeepsWhatItShows(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	svc := harness.NewService(t, nil)
	e := harness.New(t)
	e.Project("notes", harness.WebManifest(svc.URL))
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": harness.Jsonl(map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 7})}, nil)
	var d struct{ ID int64 }
	e.Must("deck.document_save", map[string]any{"project": "notes", "name": "plan.md", "kind": "doc", "text": "# Plan"}, &d)

	front := newFront(t, e.URL)
	b := Open(t)
	b.OpenVia(front.URL, e, "#/p/notes/main")
	b.until("the project screen", `document.body.innerText.includes('All notes')`)

	front.fail.Store(true)
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	b.until("the refusal of the read", `document.body.innerText.includes('the read failed in the test')`)
	if got := b.eval(`String(!!document.querySelector('[data-screen-error]') || !document.body.innerText.includes('All notes'))`); got != "false" {
		t.Fatal("a failed read of a drawn screen leaves it drawn")
	}
	front.fail.Store(false)

	b.eval(`(() => { window.__added = 0; new MutationObserver((ms) => ms.forEach((m) => m.addedNodes.forEach((n) => {
		if (n.nodeType === 1) window.__added += (n.matches('[data-block]') ? 1 : 0) + n.querySelectorAll('[data-block]').length }))).observe(document.body, { childList: true, subtree: true }); return '' })()`)
	b.eval(`(location.hash = '#/p/notes/document/` + fmt.Sprint(d.ID) + `', '')`)
	b.until("the document page", `document.querySelector('[data-edit]')`)
	time.Sleep(500 * time.Millisecond)
	if got := b.eval(`String(window.__added - document.querySelectorAll('[data-block]').length)`); got != "0" {
		t.Fatalf("a screen change does not draw the old screen again: %s blocks more", got)
	}

	b.eval(`(document.querySelector('[data-edit]').click(), '')`)
	e.Must("deck.document_save", map[string]any{"project": "notes", "name": "plan.md", "kind": "doc", "text": "# Plan of the agent"}, nil)
	b.type_(`document.querySelector('[data-draft]')`, "# Plan of the owner")
	b.eval(`(document.querySelector('[data-save-document]').click(), '')`)
	b.until("the refusal of a save over a newer revision", `document.body.innerText.includes('revision 2 now')`)
	var got struct{ Text string }
	e.Must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Text != "# Plan of the agent" {
		t.Fatalf("the owner's save does not overwrite the revision written meanwhile: %q", got.Text)
	}

	front.cut()
	b.until("the stale mark while the live connection is down", `document.querySelector('[data-stale]')`)
	e.Until("a refused reconnect", func() bool { return front.refused.Load() > 0 })
	b.eval(`(location.hash = '#/p/notes/main', '')`)
	front.heal()
	b.until("the project screen again", `document.body.innerText.includes('All notes')`)
	b.until("the stale mark to clear", `![...document.querySelectorAll('[data-stale]')].some((x) => x.offsetParent)`)
	time.Sleep(5 * time.Second)
	if n := front.streams.Load(); n != 1 {
		t.Fatalf("the page keeps one live connection after it comes back: %d", n)
	}
	if !strings.Contains(b.eval(`document.body.innerText`), "All notes") {
		t.Fatal("the screen is drawn")
	}
}

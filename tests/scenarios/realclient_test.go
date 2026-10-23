package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/app"
	"deck2/web"
)

// TestRealBrowserWebClient builds the web client and drives it in the real browser of Playwright MCP:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v (DECK_SCREENSHOTS=<dir> keeps screenshots).
func TestRealBrowserWebClient(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)

	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	clock := &Clock{now: time.Now()}
	e := newEnv(t, webAgent(), withClock(clock), withTTL)
	e.Clock = clock
	p := e.createProject("notes", webManifest(svc.URL))
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 7})}, nil)
	e.waitState(e.run(p, "work"), "waiting_answer", "done")
	e.App.Sweep(context.Background(), clock.Add(24*time.Hour+time.Minute))
	e.run(p, "work")
	e.waitFor("the fresh confirmation", func() bool { return len(e.cockpitItems()) == 2 })
	asking := e.run(p, "ask")
	e.waitFor("the question", func() bool { return len(e.cockpitItems()) == 3 })

	front := newFront(t, e.URL)
	b := newPlaywright(t)
	b.open(front.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit with three items", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].length === 3`)
	b.shot("cockpit")

	items := b.eval(`JSON.stringify([...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].map(el => [...el.querySelectorAll('button')].map(x => x.innerText.trim())))`)
	if items != `[["Details","Deny"],["Details","Allow","Deny","Deny because…"],["Details","Answer"]]` {
		t.Fatalf("an expired confirmation shows only deny, a fresh one allow first and hides a button past its end, a question its answer: %s", items)
	}
	if got := b.eval(`document.querySelector('[data-unknown="hologram"]')?.innerText ?? ''`); !strings.Contains(got, "Open in web") {
		t.Fatalf("a block the client does not know is drawn as open in web: %q", got)
	}
	if got := b.eval(`[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].innerText`); !strings.Contains(got, "post") ||
		!strings.Contains(got, "POST "+svc.URL+"/items/n2") || !strings.Contains(got, "notes") || strings.Contains(got, "notes.post") {
		t.Fatalf("a confirmation says what by its title, where and its module, the command under its details: %q", got)
	}
	b.eval(`[...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelectorAll('button')].find(x => x.innerText.includes('Details')).click(), ''`)
	b.until("the frozen call", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelector('pre')?.innerText.includes('"id": "n2"')`)

	b.eval(`[...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelectorAll('button')].find(x => x.innerText === 'Deny because…').click(), ''`)
	b.until("the reason field", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelector('input')`)
	b.eval(`(() => { const i = [...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelector('input');
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(i, 'keep me');
		i.dispatchEvent(new Event('input', { bubbles: true })); return '' })()`)
	b.eval(`[...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelectorAll('button')].find(x => x.innerText === 'Send').click(), ''`)
	b.until("the refusal of the reason", `document.body.innerText.includes('deck: refused by the test')`)
	if got := b.eval(`[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelector('input')?.value ?? ''`); got != "keep me" {
		t.Fatalf("what the owner typed stays after a refusal: %q", got)
	}
	b.eval(`[...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelectorAll('button')].find(x => x.innerText === 'Cancel').click(), ''`)

	full := front.full.Load()
	b.eval(`(() => { const x = [...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][1].querySelectorAll('button')].find(x => x.innerText === 'Allow'); x.click(); x.click(); return '' })()`)
	b.until("the allowed item to leave", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].length === 2`)
	if n, calls := front.answers.Load(), len(svc.requests()); n != 1 || calls != 1 {
		t.Fatalf("a second press while the call runs sends nothing: %d answers, %d calls", n, calls)
	}
	if front.topics.Load() == 0 || front.full.Load() != full {
		t.Fatalf("after an event only the blocks of its topic are read again: %d topic reads, %d full", front.topics.Load(), front.full.Load()-full)
	}

	b.eval(`[...[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])][0].querySelectorAll('button')].find(x => x.innerText === 'Deny').click(), ''`)
	b.until("the expired item to clear", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].length === 1`)

	b.eval(`[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].flatMap((x) => [...x.querySelectorAll('label')]).find(x => x.innerText === 'go').click(), ''`)
	b.until("answer to unlock", `!([...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].flatMap((x) => [...x.querySelectorAll('button')]).find(x => x.innerText === 'Answer')?.disabled ?? true)`)
	b.eval(`[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].flatMap((x) => [...x.querySelectorAll('button')]).find(x => x.innerText === 'Answer').click(), ''`)
	b.until("the answered question to leave", `[...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].length === 0`)
	e.waitState(asking, "done")

	front.slow.Store(true)
	late := e.run(p, "work")
	e.waitFor("the delayed read", func() bool { return front.delayed.Load() })
	for _, it := range e.cockpitItems() {
		e.must("deck.ask_answer", map[string]any{"ask": it.ID, "answer": "deny"}, nil)
	}
	e.waitState(late, "done", "error")
	time.Sleep(3 * time.Second)
	if n := b.eval(`String([...([...document.querySelectorAll('[data-block]')].filter((b) => b.querySelector('h3')?.innerText.trim() === 'Needs your decision').pop()?.querySelectorAll('[data-item]') ?? [])].length)`); n != "0" {
		t.Fatalf("a late read does not bring back what a newer one cleared: %s items", n)
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
	e.waitFor("a full read on return", func() bool { return front.full.Load() > before })
	front.cut()
	b.until("the stale mark while the live connection is down", `document.querySelector('[data-stale]')`)
	e.waitFor("a refused reconnect", func() bool { return front.refused.Load() > 0 })
	before = front.full.Load()
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	e.waitFor("a full read while the live connection is down", func() bool { return front.full.Load() > before })
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

// checkBuiltClient builds the client from its sources and fails when the embedded build is not that one.
func checkBuiltClient(t *testing.T) {
	t.Helper()
	dir, _ := filepath.Abs("../../web")
	out := t.TempDir()
	cmd := exec.Command("npm", "run", "build", "--", "--outDir", out, "--emptyOutDir")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the web client builds: %v\n%s", err, b)
	}
	built := map[string][]byte{}
	_ = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(out, path)
			built[rel], _ = os.ReadFile(path)
		}
		return nil
	})
	embedded := map[string][]byte{}
	_ = fs.WalkDir(web.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			embedded[path], _ = fs.ReadFile(web.FS(), path)
		}
		return nil
	})
	if len(built) != len(embedded) {
		t.Fatalf("web/dist is stale: run npm run build in web (%d files built, %d embedded)", len(built), len(embedded))
	}
	for name, b := range built {
		if !bytes.Equal(b, embedded[name]) {
			t.Fatalf("web/dist is stale: run npm run build in web (%s differs)", name)
		}
	}
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

type playwright struct {
	t    *testing.T
	sess *mcp.ClientSession
	dir  string
}

func newPlaywright(t *testing.T, browser ...string) *playwright {
	t.Helper()
	out := os.Getenv("DECK_SCREENSHOTS")
	if out == "" {
		out = t.TempDir()
	}
	args := append(slices.Clone(app.DefaultBrowser[1:]), "--headless", "--isolated", "--caps=vision", "--output-dir", out)
	if len(browser) > 0 {
		args = append(args, "--browser", browser[0])
	}
	cmd := exec.Command(app.DefaultBrowser[0], args...)
	cmd.SysProcAttr, cmd.Dir = &syscall.SysProcAttr{Setpgid: true}, out
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "deck-test", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatalf("Playwright MCP starts: %v", err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	return &playwright{t: t, sess: sess, dir: out}
}

func (b *playwright) call(tool string, args map[string]any) string {
	b.t.Helper()
	text, failed := b.try(tool, args)
	if failed {
		b.t.Fatalf("%s: %s", tool, text)
	}
	return text
}

// try calls a tool of the browser and says whether it failed, such as an evaluation a navigation cut short.
func (b *playwright) try(tool string, args map[string]any) (string, bool) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := b.sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		b.t.Fatalf("%s: %v", tool, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

func (b *playwright) open(u string) { b.call("browser_navigate", map[string]any{"url": u}) }

// eval runs a function in the page and returns its result, which must be a string.
func (b *playwright) eval(js string) string {
	b.t.Helper()
	out := b.call("browser_evaluate", map[string]any{"function": "() => { const W = document.querySelector('[data-block]') ?? document; return " + js + " }"})
	_, after, ok := strings.Cut(out, "### Result")
	if !ok {
		b.t.Fatalf("evaluate gave no result: %s", out)
	}
	line := strings.TrimSpace(after)
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	var s string
	if json.Unmarshal([]byte(line), &s) != nil {
		return line
	}
	return s
}

// look picks a value of a dropdown of the look on the Interface tab, opening the tab when it is not open.
func (b *playwright) look(name, value string) {
	b.t.Helper()
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const at = page.locator('[data-look="` + name + `"]');
		if (!(await at.isVisible())) await page.locator('[role=tab]').filter({hasText: /^(Interface|Интерфейс)$/}).click();
		await at.click();
		await page.locator('[role=option][value="` + value + `"]:visible').click(); }`})
	b.until(name+" "+value, `document.querySelector('[data-look="`+name+`"][data-value="`+value+`"]')`)
}

func (b *playwright) until(what, cond string) {
	b.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, failed := b.try("browser_evaluate", map[string]any{"function": "() => { const W = document.querySelector('[data-block]') ?? document; return String(!!(" + cond + ")) }"})
		if failed && !strings.Contains(out, "Execution context was destroyed") {
			b.t.Fatalf("browser_evaluate: %s", out)
		}
		if _, after, _ := strings.Cut(out, "### Result"); !failed && strings.HasPrefix(strings.TrimSpace(after), `"true"`) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	b.shot("failed")
	b.t.Fatalf("no %s in the page:\n%s", what, b.eval("document.body.innerText"))
}

func (b *playwright) shot(name string) {
	b.call("browser_take_screenshot", map[string]any{"filename": fmt.Sprintf("web-%s.png", name), "fullPage": true})
}

// pressIn clicks the button of a verb in a row or an item, also when it waits in the row's more menu.
func pressIn(scope, verb string) string {
	return `(() => { const s = ` + scope + `; let b = s.querySelector('[data-verb="` + verb + `"]');
		if (!b) { const m = s.querySelector('[data-more]'); m.click(); b = document.querySelector('[data-more-menu="' + m.dataset.more + '"] [data-verb="` + verb + `"]'); }
		b.click(); return '' })()`
}

// reviewAndEnable presses Review and enable on the row of a draft in Settings, then Enable on the page of what it
// may do.
func (b *playwright) reviewAndEnable(name string) {
	b.eval(pressIn(itemOf(name, "draft"), "review"))
	b.until("the page of what "+name+" may do", `location.hash.startsWith('#/deck/draft') && document.querySelector('[data-verb="enable"]')`)
	b.eval(`(document.querySelector('[data-verb="enable"]').click(), '')`)
}

// itemOf finds a list item by its title and a part of its text.
func itemOf(title, part string) string {
	return `[...document.querySelectorAll('[data-item]')].find((x) => x.innerText.split('\n')[0].trim() === '` + title + `' && x.innerText.includes('` + part + `'))`
}

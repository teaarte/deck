package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/tests/harness"
	"deck2/web"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// builtClient says once for the package what is wrong with the embedded client, empty when it is the build of web.
var builtClient = sync.OnceValue(func() string {
	out, err := os.MkdirTemp("", "deck-web-")
	if err != nil {
		return err.Error()
	}
	defer os.RemoveAll(out)
	cmd := exec.Command("npm", "run", "build", "--", "--outDir", out, "--emptyOutDir")
	cmd.Dir = filepath.Join(harness.Root, "web")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("the web client builds: %v\n%s", err, b)
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
		return fmt.Sprintf("web/dist is stale: run npm run build in web (%d files built, %d embedded)", len(built), len(embedded))
	}
	for name, b := range built {
		if !bytes.Equal(b, embedded[name]) {
			return fmt.Sprintf("web/dist is stale: run npm run build in web (%s differs)", name)
		}
	}
	return ""
})

// checkBuiltClient fails when the embedded client is not the build of its sources, built once for the package.
func checkBuiltClient(t *testing.T) {
	t.Helper()
	if why := builtClient(); why != "" {
		t.Fatal(why)
	}
}

type playwright struct {
	t    *testing.T
	sess *mcp.ClientSession
	dir  string
	kind string
	// base is the address of deck the page signed in at: https in Chromium, by the cookie of its passkey; plain http
	// in WebKit, by the key of a launcher.
	base string
	// paired says the virtual authenticator of the browser holds a passkey of deck.
	paired bool
}

// Open starts the browser of a test on deck's client, after it checks that the embedded client is the build of web.
func Open(t *testing.T, kind ...string) *playwright {
	t.Helper()
	checkBuiltClient(t)
	return newPlaywright(t, kind...)
}

// newPlaywright starts Playwright MCP headless, its screenshots in DECK_SCREENSHOTS when that is set.
func newPlaywright(t *testing.T, browser ...string) *playwright {
	t.Helper()
	// Each test keeps its pictures in a folder of its own, as tests run side by side.
	out := t.TempDir()
	if dir := os.Getenv("DECK_SCREENSHOTS"); dir != "" {
		out = filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "-"))
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	args := append(slices.Clone(app.DefaultBrowser[1:]), "--headless", "--isolated", "--caps=vision", "--ignore-https-errors", "--output-dir", out)
	kind := "chrome"
	if len(browser) > 0 {
		kind = browser[0]
		args = append(args, "--browser", kind)
	} else {
		// Chromium registers a service worker only from an origin whose certificate it takes, which the TLS of the test
		// is not.
		config := filepath.Join(t.TempDir(), "playwright.json")
		if err := os.WriteFile(config, []byte(`{"browser": {"launchOptions": {"args": ["--ignore-certificate-errors"]}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--config", config)
	}
	cmd := exec.Command(app.DefaultBrowser[0], args...)
	cmd.SysProcAttr, cmd.Dir = &syscall.SysProcAttr{Setpgid: true}, out
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "deck-test", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}, nil)
	if err != nil {
		t.Fatalf("Playwright MCP starts: %v", err)
	}
	// The browser goes before the servers of the test, whole and in bounded time, so no page keeps a connection open.
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			_ = sess.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Log("Playwright MCP did not close in 10 s; killed")
		}
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	return &playwright{t: t, sess: sess, dir: out, kind: kind}
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
		if (!(await at.isVisible())) { await page.evaluate(() => { location.hash = '#/deck/look' }); await at.waitFor(); }
		await at.click();
		await page.locator('[role=option][value="` + value + `"]:visible').click(); }`})
	b.until(name+" "+value, `document.querySelector('[data-look="`+name+`"][data-value="`+value+`"]')`)
}

// pageWait is how long the page has to show what a test waits for; a page shows it in seconds.
const pageWait = 30 * time.Second

func (b *playwright) until(what, cond string) {
	b.t.Helper()
	done := false
	defer func() {
		if !done {
			b.shot("failed")
			b.t.Logf("no %s in the page:\n%s", what, b.eval("document.body.innerText"))
		}
	}()
	harness.UntilWithin(b.t, pageWait, what+" in the page", func() bool {
		out, failed := b.try("browser_evaluate", map[string]any{"function": "() => { const W = document.querySelector('[data-block]') ?? document; return String(!!(" + cond + ")) }"})
		if failed && !strings.Contains(out, "Execution context was destroyed") {
			done = true
			b.t.Fatalf("browser_evaluate: %s", out)
		}
		_, after, _ := strings.Cut(out, "### Result")
		done = !failed && strings.HasPrefix(strings.TrimSpace(after), `"true"`)
		return done
	})
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

// Open opens deck of e as its owner with rest, more of the query then the route: Chromium signs in at deck's https
// address with a passkey of its virtual authenticator, WebKit with a key as the launcher's window does.
func (b *playwright) Open(e *harness.Env, rest string) {
	b.t.Helper()
	if b.kind == "webkit" {
		b.OpenVia(e.URL, e, rest)
		return
	}
	if base := e.Https(); b.base != base {
		b.signIn(e, base, rest)
		return
	}
	b.open(b.base + "/?owner=1" + rest)
}

// OpenVia is Open through a server before deck at base, as the launcher's window opens it; the page loads anew, as
// an address that differs from the one before only after # would not load it.
func (b *playwright) OpenVia(base string, e *harness.Env, rest string) {
	b.t.Helper()
	b.base = base
	b.open("about:blank")
	b.open(e.OwnerURL(base, rest))
}

// press clicks a button once the call has settled, so the tool does not wait out the stream of events the press
// opens: it waits for each request the call starts within half a second.
func (b *playwright) press(sel string) {
	b.eval(`(setTimeout(() => document.querySelector('` + sel + `').click(), 700), '')`)
}

// signIn signs the browser in at base and opens rest: it pairs by a link of deck with a new passkey the first time,
// as the owner's phone does, and signs in with that passkey after.
func (b *playwright) signIn(e *harness.Env, base, rest string) {
	b.t.Helper()
	if !b.paired {
		b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  globalThis.deckAuthn = await page.context().newCDPSession(page);
  await globalThis.deckAuthn.send('WebAuthn.enable');
  await globalThis.deckAuthn.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal',
    hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
}`})
		var link struct{ Code string }
		e.Must("deck.pair_link", map[string]any{"name": "the test's browser"}, &link)
		query, route, _ := strings.Cut(rest, "#")
		b.open(base + "/?owner=1" + query + "#pair=" + link.Code + route)
		b.until("the page that pairs this browser", `document.querySelector('[data-signin="pair"]:not([disabled])')`)
		b.press(`[data-signin="pair"]`)
		b.paired = true
	} else {
		b.open(base + "/?owner=1" + rest)
		b.until("the page that signs in", `document.querySelector('[data-signin="passkey"]')`)
		b.press(`[data-signin="passkey"]`)
	}
	b.until("the client signed in", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)
	b.base = base
}

// file puts a file into a file input as picking it does.
func (b *playwright) file(el, name, content string) {
	b.t.Helper()
	b.eval(fmt.Sprintf(`(() => { const i = %s; const dt = new DataTransfer(); dt.items.add(new File([%q], %q, { type: 'text/plain' }));
		i.files = dt.files; i.dispatchEvent(new Event('change', { bubbles: true })); return '' })()`, el, content, name))
}

// pick opens a select and clicks the option with the label in the list of that select.
func (b *playwright) pick(sel, label string) {
	b.t.Helper()
	b.eval(fmt.Sprintf(`(document.querySelector(%q).click(), '')`, sel))
	opt := fmt.Sprintf(`(() => { const id = document.querySelector(%q).getAttribute('aria-controls'); const box = id && document.getElementById(id);
		return [...(box || document).querySelectorAll('[role=option]')].find((o) => o.innerText.trim() === %q) })()`, sel, label)
	b.until("the option "+label, opt)
	b.eval(fmt.Sprintf(`(%s.click(), '')`, opt))
}

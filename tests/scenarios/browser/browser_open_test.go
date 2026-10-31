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
	"syscall"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/tests/harness"
	"deck2/web"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// checkBuiltClient builds the client from its sources and fails when the embedded build is not that one.
func checkBuiltClient(t *testing.T) {
	t.Helper()
	dir := filepath.Join(harness.Root, "web")
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

type playwright struct {
	t    *testing.T
	sess *mcp.ClientSession
	dir  string
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
		if (!(await at.isVisible())) { await page.evaluate(() => { location.hash = '#/deck/look' }); await at.waitFor(); }
		await at.click();
		await page.locator('[role=option][value="` + value + `"]:visible').click(); }`})
	b.until(name+" "+value, `document.querySelector('[data-look="`+name+`"][data-value="`+value+`"]')`)
}

func (b *playwright) until(what, cond string) {
	b.t.Helper()
	done := false
	defer func() {
		if !done {
			b.shot("failed")
			b.t.Logf("no %s in the page:\n%s", what, b.eval("document.body.innerText"))
		}
	}()
	harness.Until(b.t, what+" in the page", func() bool {
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

// Open opens deck of e as its owner, rest after the sign-in: more of the query, then the route of the client.
func (b *playwright) Open(e *harness.Env, rest string) { b.open(e.OwnerURL(e.URL, rest)) }

// OpenVia is Open through a server before deck at base.
func (b *playwright) OpenVia(base string, e *harness.Env, rest string) {
	b.open(e.OwnerURL(base, rest))
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

// Package shots draws a screen of deck in deck's own browser for the master to look at: a picture as a file of
// the calling process and how long the screen took to draw.
package shots

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/files"
	"deck2/internal/home"
	"deck2/internal/mcphost"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
)

// Service is the browser deck opens its own pages in; Lend gives it a token of the caller's rights for a while.
type Service struct {
	Host   *mcphost.Host
	Argv   []string
	Home   home.Layout
	Base   string
	Lend   func(registry.Actor, time.Duration) string
	Return func(string)
	Themed func(ctx context.Context, workspace int64, name string) bool

	mu sync.Mutex
}

// Key names the browser of the shots on a device; it has no profile and reaches only deck.
func Key(device string) string { return "screen-shot-" + device }

func (s *Service) dir(name string) string { return filepath.Join(s.Home.Browser(), "shot", name) }

func (s *Service) spec(device string) mcphost.Spec {
	args := append(slices.Clone(s.Argv[1:]), "--headless", "--isolated", "--block-service-workers", "--output-dir", s.dir("out"))
	if device == "phone" {
		args = append(args, "--device", "iPhone 15")
	} else {
		args = append(args, "--viewport-size", "1280x820")
	}
	return mcphost.Spec{Command: s.Argv[0], Args: args, Env: sandbox.Kept("HOME", "TMPDIR", "XDG_"), Dir: s.dir("work"), Write: []string{s.dir("out")}}
}

type shotIn struct {
	Address string `desc:"The address of the screen in deck's client, such as #/p/notes/main?tab=Ideas or #/deck/settings?dialog=connections; #/ is the tiles"`
	Scheme  string `enum:"light|dark" opt:"" desc:"The scheme; the device's own when left out"`
	Theme   string `opt:"" desc:"A theme to wear for the shot, as a preview; the theme in use when left out"`
	Device  string `enum:"computer|phone" opt:"" desc:"A computer's window, 1280×820, or a phone; a computer when left out"`
}

// ready is true in the page once its screen is drawn: no block waits for data, fonts and pictures are in, or the
// screen failed; its value is the ms since the page began to load.
const ready = `() => {
  if (document.querySelector('[data-screen-error]')) return performance.now();
  if (!document.querySelector('[data-screen-view]:not([aria-busy]) [data-screen], [data-apps]')) return 0;
  if (document.querySelector('[data-awaits], [data-pending]') || document.fonts.status !== 'loaded') return 0;
  if ([...document.images].some((i) => !i.complete && i.loading !== 'lazy')) return 0;
  return performance.now();
}`

var address = regexp.MustCompile(`^#/[^\s]*$`)

// shot opens the address under the caller's rights, waits for the screen to be drawn and keeps its picture.
func (s *Service) shot(ctx context.Context, act registry.Actor, in shotIn) (any, error) {
	if !address.MatchString(in.Address) {
		return nil, registry.Invalid("deck: the address of a screen begins with #/, such as #/deck/settings")
	}
	if in.Theme != "" && s.Themed != nil && !s.Themed(ctx, act.Workspace, in.Theme) {
		return nil, registry.NotFound("deck: no theme %q", in.Theme)
	}
	if len(s.Argv) == 0 {
		return nil, registry.Refuse("deck: deck runs without a browser; set browser in its config")
	}
	device := in.Device
	if device == "" {
		device = "computer"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, spec := Key(device), s.spec(device)
	// The browser starts before the token is lent, so a slow first start does not outlast it.
	if _, err := s.Host.Tools(ctx, key, spec); err != nil {
		return nil, fmt.Errorf("deck: the browser of the shots: %w", err)
	}
	token := s.Lend(act, 2*time.Minute)
	defer s.Return(token)
	q := url.Values{"token": {token}, "shot": {"1"}}
	if in.Theme != "" {
		q.Set("theme", in.Theme)
	}
	if in.Scheme != "" {
		q.Set("scheme", in.Scheme)
	}
	page := strings.TrimRight(s.Base, "/") + "/?" + q.Encode() + in.Address
	// A phone's full-page picture leaves the frames of custom blocks blank, so its window grows to the page instead.
	code := fmt.Sprintf(`async (page) => {
  await page.goto(%q);
  const drawn = await page.waitForFunction(%s, null, { timeout: 30000, polling: 100 });
  const ms = await drawn.jsonValue();
  await page.waitForTimeout(250);
  await page.waitForFunction(%s, null, { timeout: 10000, polling: 100 });
  const window = page.viewportSize();
  const tall = %t ? Math.min(await page.evaluate(() => document.documentElement.scrollHeight), 8000) : 0;
  if (tall > window.height) {
    await page.setViewportSize({ width: window.width, height: tall });
    try {
      await page.waitForTimeout(250);
      await page.waitForFunction(%s, null, { timeout: 10000, polling: 100 });
    } catch (e) {
      await page.setViewportSize(window);
      throw e;
    }
  }
  const error = await page.evaluate(() => document.querySelector('[data-screen-error]')?.innerText ?? '');
  return JSON.stringify({ ms: Math.round(ms), error, window, grown: tall > window.height });
}`, page, ready, ready, device == "phone", ready)
	res, err := s.call(ctx, key, spec, "browser_run_code_unsafe", map[string]any{"code": code})
	if err != nil {
		return nil, err
	}
	var drawn struct {
		MS     int64           `json:"ms"`
		Error  string          `json:"error"`
		Window json.RawMessage `json:"window"`
		Grown  bool            `json:"grown"`
	}
	if err := json.Unmarshal([]byte(result(res)), &drawn); err != nil {
		return nil, fmt.Errorf("deck: the screen did not draw: %s", strings.TrimSpace(text(res)))
	}
	pic, err := s.call(ctx, key, spec, "browser_take_screenshot", map[string]any{"type": "png", "fullPage": true})
	if drawn.Grown {
		_, _ = s.call(ctx, key, spec, "browser_run_code_unsafe", map[string]any{"code": fmt.Sprintf("async (page) => { await page.setViewportSize(%s); }", drawn.Window)})
	}
	// The page leaves, so it hears no more of the workspace once its token is back.
	_, _ = s.call(ctx, key, spec, "browser_navigate", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, err
	}
	png, err := s.picture(pic)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ms": drawn.MS, "type": "image/png", "size": len(png)}
	if drawn.Error != "" {
		out["error"] = drawn.Error
	}
	if act.ProcessID == 0 {
		out["kept"] = false
		return out, nil
	}
	name, _, err := files.Save(s.Home, act.ProcessID, "screen", "image/png", bytes.NewReader(png), 1<<28)
	if err != nil {
		return nil, err
	}
	out["file"], out["path"] = "results/"+name, filepath.Join(s.Home.Results(act.ProcessID), name)
	return out, nil
}

func (s *Service) call(ctx context.Context, key string, spec mcphost.Spec, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	raw, _ := json.Marshal(args)
	res, err := s.Host.Call(ctx, key, spec, tool, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("deck: the browser of the shots: %w", err)
	}
	if res.IsError {
		return nil, fmt.Errorf("deck: the browser of the shots: %s", strings.TrimSpace(text(res)))
	}
	return res, nil
}

// picture is the PNG a screenshot answered with, or the file it saved in its folder.
func (s *Service) picture(res *mcp.CallToolResult) ([]byte, error) {
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok && len(img.Data) > 0 {
			return img.Data, nil
		}
	}
	if m := saved.FindStringSubmatch(text(res)); m != nil {
		path := m[1]
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.dir("out"), path)
		}
		if rel, err := filepath.Rel(s.dir("out"), path); err == nil && !strings.HasPrefix(rel, "..") {
			return os.ReadFile(path)
		}
	}
	return nil, errors.New("deck: the browser of the shots gave no picture")
}

var saved = regexp.MustCompile(`([^\s()\[\]]+\.png)`)

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text + "\n")
		}
	}
	return b.String()
}

// result is what a run of code returned: the JSON after "### Result", unquoted once when Playwright quoted it.
func result(res *mcp.CallToolResult) string {
	t := text(res)
	if _, after, ok := strings.Cut(t, "### Result"); ok {
		t = after
	}
	t = strings.TrimSpace(t)
	if i := strings.Index(t, "\n###"); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	var inner string
	if json.Unmarshal([]byte(t), &inner) == nil {
		return inner
	}
	return t
}

// Commands are the shot of a screen.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{registry.Def("screen_capture", "Screen shot",
		"Draws a screen of deck's client in deck's own browser under the caller's rights, as a computer or a phone, light or dark, in a theme to try; answers the picture as a file of the calling process (file, and path to read it) and ms, how long the screen took to draw, with error when it failed.",
		registry.OwnerSide, s.shot)}
}

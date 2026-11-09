package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserTheWorkOfATopic: the work a topic started goes on in a strip above the input, one that waits amber,
// the ended under Recent; the history marks the topics that work, on a desk and a phone.
func TestRealBrowserTheWorkOfATopic(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	hold := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Command() == "work" && strings.Contains(c.Req.Prompt, "TASKHOLD"):
			select {
			case <-hold:
			case <-ctx.Done():
			}
			return provider.Result{Text: "held"}
		case c.Command() == "work" && strings.Contains(c.Req.Prompt, "TASKQUICK"):
			return provider.Result{Text: "quick"}
		case c.Command() == "work":
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which collection?", "options": []map[string]any{{"label": "tasks"}, {"label": "notes"}}}}})
			return provider.Result{Text: "asked"}
		default:
			var in struct{ Topic int64 }
			if m := harness.InputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
				_ = json.Unmarshal([]byte(m[1]), &in)
			}
			for _, task := range []string{"TASKHOLD", "TASKASK", "TASKQUICK"} {
				if strings.Contains(c.Req.Prompt, strings.ToLower(task)) {
					if out, bad := c.Tool(ctx, "master__work", map[string]any{"task": task, "topic": in.Topic}); bad {
						return provider.Result{Text: "refused: " + out}
					}
					break
				}
			}
		}
		return provider.Result{Text: "ok"}
	}))
	defer close(hold)
	e.MasterSaid(e.Say(0, "a quiet topic"), 1)
	asked := e.Say(0, "do taskask")
	e.MasterSaid(asked, 1)
	e.WaitAsk()
	held := e.Say(0, "do taskquick")
	e.MasterSaid(held, 1)
	e.Until("the quick work ended", func() bool {
		var v struct{ Recent []any }
		e.Must("deck.chat_turn_get", map[string]any{"topic": fmt.Sprint(held)}, &v)
		return len(v.Recent) == 1
	})
	e.Say(held, "do taskhold")
	e.MasterSaid(held, 2)

	b := Open(t)
	in := `(document.querySelector('[data-chat-phone]') || document)`
	pick := func(title string) {
		b.eval(`([...document.querySelectorAll('[role=option] [data-topic-line]')].find((x) => x.querySelector('[data-topic-title]').innerText === '` + title + `').closest('[role=option]').click(), '')`)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/cockpit")
		toggle := map[string]string{"desktop": "[data-chat-toggle]", "390": `[data-tab="deki"]`}[size.name]
		b.until(size.name+": the chat button", `document.querySelector('`+toggle+`')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent) || document.querySelector('` + toggle + `').click(), '')`)
		b.until(size.name+": the history", in+`.querySelector('[data-topics]')`)
		b.eval(`(` + in + `.querySelector('[data-topics]').click(), '')`)
		b.until(size.name+": the topics", `document.querySelector('[role=option] [data-topic-line]')`)
		pick("do taskquick")
		b.until(size.name+": the topic that works", in+`.querySelector('[data-topics]')?.value === 'do taskquick'`)
		b.until(size.name+": the strip says one work runs with its command, mark and timer", `(() => { const w = `+in+`.querySelector('[data-chat-work]'); if (!w) return false;
			const p = w.querySelector('[data-chat-work-pill]'); return w.dataset.chatWork === '1' && p && !p.dataset.waits && p.innerText.includes('master.work')
				&& p.querySelector('[data-logo], [data-logo-letter]') && /^\d+:\d\d$/.test(p.querySelector('[data-chat-work-fact]').innerText) && p.querySelector('[data-verb="open"]') })()`)
		b.until(size.name+": the closed history says another topic works", in+`.querySelector('[data-topics-busy][data-waits]')`)
		b.until(size.name+": the topic picked wears its dot", in+`.querySelector('[data-topic-busy]')`)
		if size.name == "390" {
			b.eval(`(` + in + `.querySelector('[data-chat-work-recent]').click(), '')`)
			b.until("390: the strip is one line that scrolls sideways", `(() => { const w = `+in+`.querySelector('[data-chat-work]'); const p = w.querySelector('[data-chat-work-pill]');
				return getComputedStyle(w).flexWrap === 'nowrap' && w.scrollWidth > w.clientWidth && w.getBoundingClientRect().height < p.getBoundingClientRect().height + 8 })()`)
		} else {
			b.eval(`(` + in + `.querySelector('[data-chat-work-recent]').click(), '')`)
		}
		b.until(size.name+": the recent work under Recent, with how it ended", in+`.querySelector('[data-chat-work-pill][data-ended]')?.innerText.includes('done')`)
		b.shot("f3-strip-" + size.name)
		b.eval(`(` + in + `.querySelector('[data-topics]').click(), '')`)
		b.until(size.name+": the history marks the topics that work, amber the one that waits, and says how long", `(() => {
			const line = (title) => [...document.querySelectorAll('[role=option] [data-topic-line]')].find((x) => x.querySelector('[data-topic-title]').innerText === title);
			const h = line('do taskquick'), a = line('do taskask'), q = line('a quiet topic');
			return h && a && q && h.querySelector('[data-topic-busy]:not([data-waits])') && /\d+:\d\d/.test(h.querySelector('[data-topic-working]')?.innerText ?? '')
				&& a.querySelector('[data-topic-busy][data-waits]') && a.querySelector('[data-topic-working]') && !q.querySelector('[data-topic-busy]') })()`)
		b.shot("f3-topics-" + size.name)
		pick("do taskask")
		b.until(size.name+": the work that waits for the owner is amber and says so", `(() => { const p = `+in+`.querySelector('[data-chat-work-pill]');
			return p && p.dataset.waits && `+in+`.querySelector('[data-topics]').value === 'do taskask' })()`)
		b.shot("f3-waits-" + size.name)
		b.eval(`(` + in + `.querySelector('[data-chat-work-pill] [data-verb="open"]').click(), '')`)
		b.until(size.name+": the pill opens its process", `location.hash.includes('process')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].find((x) => x.offsetParent)?.click(), '')`)
	}
}

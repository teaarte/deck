package browser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserTheFootOfTheChatStandsOnOneLine: the turn over the input and the input share their edges, radius
// and one left line; the chip of the screen says it in words, the model of the turn wears its mark.
func TestRealBrowserTheFootOfTheChatStandsOnOneLine(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "think long") {
			<-ctx.Done()
		}
		return provider.Result{Text: "ok"}
	}))
	e.Project("web", harness.WebNotes)
	job := e.Start("web", "score", map[string]any{"title": "alpha"})
	e.WaitState(job, "done")
	e.Say(0, "think long about it")

	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/process/"+fmt.Sprint(job))
		toggle := map[string]string{"desktop": "[data-chat-toggle]", "390": `[data-tab="deki"]`}[size.name]
		b.until(size.name+": the chat button", `document.querySelector('`+toggle+`')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent) || document.querySelector('` + toggle + `').click(), '')`)
		b.until(size.name+": the chat open", `[...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent)`)
		root := `(document.querySelector('[data-chat-phone]') || document)`
		b.until(size.name+": the turn over the input", root+`.querySelector('[data-turn="running"] [data-turn-head]') && `+root+`.querySelector('[data-context]')`)
		b.shot("f9-foot-" + size.name)
		if got := b.eval(`(() => { const r = ` + root + `; const box = (x) => x.getBoundingClientRect();
			const head = box(r.querySelector('[data-turn-head]')), card = box(r.querySelector('[data-composer]'));
			const chip = box(r.querySelector('[data-context]')), text = box(r.querySelector('[data-chat-input]')), add = box(r.querySelector('[data-composer-add]'));
			const mic = box(r.querySelector('[data-mic]')), send = box(r.querySelector('[data-chat-send]'));
			const field = r.querySelector('[data-chat-input]'), bg = getComputedStyle(field).backgroundColor;
			const out = [];
			if (Math.abs(head.left - card.left) > 1 || Math.abs(head.right - card.right) > 1) out.push('edges ' + [head.left, card.left, head.right, card.right]);
			if (getComputedStyle(r.querySelector('[data-turn-head]')).borderTopLeftRadius !== getComputedStyle(r.querySelector('[data-composer]')).borderTopLeftRadius) out.push('radius');
			if (Math.max(chip.left, text.left, add.left) - Math.min(chip.left, text.left, add.left) > 2) out.push('left line ' + [chip.left, text.left, add.left]);
			if (bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent') out.push('field background ' + bg);
			if (mic.width !== send.width || mic.height !== send.height || Math.abs((mic.top + mic.bottom) - (send.top + send.bottom)) > 1) out.push('mic and send');
			if (card.right - send.right !== add.left - card.left) out.push('insets ' + [add.left - card.left, card.right - send.right]);
			const words = r.querySelector('[data-context]').innerText;
			if (/process/.test(words) || !words.includes('#` + fmt.Sprint(job) + `')) out.push('chip ' + words);
			if (!r.querySelector('[data-turn-model] [data-logo], [data-turn-model] [data-logo-letter]')) out.push('model mark');
			return out.join('; ') || 'ok' })()`); got != `"ok"` && got != "ok" {
			t.Fatalf("%s: the foot of the chat: %s", size.name, got)
		}
	}
}

package docs

import (
	"fmt"
	"slices"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// Edit replaces each old part of the text with its new one; each old part must occur in the text exactly once, as
// it is or else with runs of spaces and line breaks taken as one, and the parts may not overlap.
func Edit(text string, old, new []string) (string, error) {
	if len(old) != len(new) {
		return "", registry.Invalid("deck: %d old texts and %d new ones; give one new text for each old one", len(old), len(new))
	}
	type part struct {
		at, end, i int
		new        string
	}
	parts := make([]part, 0, len(old))
	var flat *spaced
	for i, o := range old {
		if o == "" {
			return "", registry.Invalid("deck: old text %d is empty", i+1)
		}
		switch n := strings.Count(text, o); {
		case n == 1:
			at := strings.Index(text, o)
			parts = append(parts, part{at, at + len(o), i, new[i]})
			continue
		case n > 1:
			return "", registry.Invalid("deck: old text %d occurs %d times; take more of its surroundings so it occurs once: %q", i+1, n, untrusted.Cut(o, 80))
		}
		if flat == nil {
			flat = spacedOf(text)
		}
		core := strings.TrimSpace(o)
		f := spacedOf(core).text
		n := strings.Count(flat.text, f)
		if core == "" {
			n = 0
		}
		switch n {
		case 1:
			k := strings.Index(flat.text, f)
			lead, trail := o[:strings.Index(o, core)], o[strings.Index(o, core)+len(core):]
			nw := strings.TrimSuffix(strings.TrimPrefix(new[i], lead), trail)
			parts = append(parts, part{flat.at[k], flat.at[k+len(f)-1] + 1, i, nw})
		case 0:
			return "", registry.Invalid("deck: old text %d is not in the current text, not even with spaces and line breaks taken as one: %q%s",
				i+1, untrusted.Cut(o, 80), flat.closest(text, f))
		default:
			return "", registry.Invalid("deck: old text %d occurs %d times with spaces and line breaks taken as one; take more of its surroundings so it occurs once: %q", i+1, n, untrusted.Cut(o, 80))
		}
	}
	slices.SortFunc(parts, func(a, b part) int { return a.at - b.at })
	var b strings.Builder
	last, prev := 0, 0
	for _, p := range parts {
		if p.at < last {
			a, b := min(p.i, prev), max(p.i, prev)
			return "", registry.Invalid("deck: old text %d overlaps old text %d (%q); give the two as one old text", b+1, a+1, untrusted.Cut(old[a], 80))
		}
		prev = p.i
		b.WriteString(text[last:p.at])
		b.WriteString(p.new)
		last = p.end
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

// spaced is a text with each run of spaces, tabs and line breaks as one space, and where each byte of it is in the
// text.
type spaced struct {
	text string
	at   []int
}

func spacedOf(s string) *spaced {
	var b strings.Builder
	at := make([]int, 0, len(s))
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			b.WriteByte(s[i])
			at = append(at, i)
			continue
		}
		b.WriteByte(' ')
		at = append(at, i)
		for i+1 < len(s) && isSpace(s[i+1]) {
			i++
		}
	}
	return &spaced{b.String(), at}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// closest names the line of the text where the longest start or end of a part is found, and the text there.
func (sp *spaced) closest(text, part string) string {
	longest := func(cut func(n int) string) (int, int) {
		lo, hi, at := 0, len(part), -1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if k := strings.Index(sp.text, cut(mid)); k >= 0 {
				lo, at = mid, k
			} else {
				hi = mid - 1
			}
		}
		return lo, at
	}
	n, at := longest(func(n int) string { return part[:n] })
	if m, k := longest(func(n int) string { return part[len(part)-n:] }); m > n {
		n, at = m, max(0, k-(len(part)-m))
	}
	if n < 8 || at < 0 {
		return ""
	}
	from := sp.at[at]
	from = strings.LastIndexByte(text[:from], '\n') + 1
	to := min(len(text), sp.at[at]+len(part)+40)
	return fmt.Sprintf("; the closest is at line %d: %s", strings.Count(text[:from], "\n")+1, untrusted.Line(text[from:to], len(part)+80))
}

package documents

import (
	"fmt"
	"html"
	"strings"
	"unicode"

	"deck2/internal/manifest"
)

// box is a part of memory on the picture, at a cell of its grid.
type box struct {
	col, row    int
	title, line string
	muted       bool
}

// The grid of the picture: a cell's place and the size of a box in it.
const (
	cellW, cellH = 240, 110
	boxW, boxH   = 204, 72
	// textW is the room of a line of text in a box.
	textW      = boxW - 24
	padX, padY = 18, 20
)

func (b box) x() int { return padX + b.col*cellW }
func (b box) y() int { return padY + b.row*cellH }

// edge is the point where the line from the middle of a toward the middle of b leaves a.
func edge(a, b box) (float64, float64) {
	ax, ay := float64(a.x()+boxW/2), float64(a.y()+boxH/2)
	dx, dy := float64(b.x()+boxW/2)-ax, float64(b.y()+boxH/2)-ay
	sx, sy := float64(boxW/2)/abs(dx), float64(boxH/2)/abs(dy)
	t := min(sx, sy)
	return ax + dx*t, ay + dy*t
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	if v == 0 {
		return 1e-9
	}
	return v
}

// mapSVG draws what feeds what in deck's memory, with the numbers of the map, in the owner's words.
func mapSVG(m memoryMap, ws map[string]string) string {
	tr := func(s string) string { return manifest.Tr(ws, s) }
	say := func(format string, a ...any) string { return fmt.Sprintf(tr(format), a...) }
	node := map[string]nodeRow{}
	for _, n := range m.Nodes {
		node[n.ID] = n
	}
	var owner memoryRow
	if len(m.Projects) > 0 {
		owner = m.Projects[0]
	}
	boxes := map[string]box{
		"words":     {0, 0, tr("The owner's words"), tr("chats, answers, yes"), false},
		"facts":     {1, 0, tr("Facts and index"), say("%d facts, %d of %d lines", owner.Facts, owner.IndexLines, owner.IndexLimit), false},
		"turns":     {2, 0, tr("Every turn"), tr("gets the index of its memory"), false},
		"episodes":  {0, 1, tr("Episodes"), tr("runs, failures, messages"), false},
		"sleep":     {1, 1, tr("Sleep"), tr("every night: lessons, proposals"), false},
		"lessons":   {2, 1, tr("Lessons"), say("%d lessons", len(m.Lessons)), false},
		"runs":      {3, 1, tr("Runs of a command"), tr("get the lessons of the command"), false},
		"proposals": {1, 2, tr("Proposals"), tr("changes of facts and skills"), false},
		"yes":       {2, 2, tr("Your yes"), tr("saves a fact or a skill"), false},
		"skills":    {3, 2, tr("Skills"), say("%d skills, %d changed by you", m.skills, m.edited), false},
		"search":    {0, 3, "memory_search", tr("facts, lessons, skills, summaries, episodes"), false},
		"summaries": {1, 3, tr("Summaries of topics"), say("%d topics summed up", m.summaries), false},
		"cli":       {3, 3, tr("Own memory of the agents"), tr("off"), true},
	}
	arrows := []struct {
		from, to string
		dashed   bool
	}{
		{"words", "facts", false}, {"facts", "turns", false}, {"episodes", "sleep", false}, {"sleep", "lessons", false},
		{"lessons", "runs", false}, {"sleep", "proposals", false}, {"proposals", "yes", false}, {"yes", "skills", false},
		{"skills", "runs", false}, {"search", "episodes", true}, {"search", "summaries", true},
	}
	w, h := padX*2+3*cellW+boxW, padY*2+3*cellH+boxH
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" font-family="system-ui, sans-serif">`, w, h)
	b.WriteString(`<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">` +
		`<path d="M0,0 L10,5 L0,10 z" fill="#64748b"/></marker></defs>`)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#ffffff"/>`, w, h)
	for _, a := range arrows {
		x1, y1 := edge(boxes[a.from], boxes[a.to])
		x2, y2 := edge(boxes[a.to], boxes[a.from])
		dash := ""
		if a.dashed {
			dash = ` stroke-dasharray="5 4"`
		}
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="#64748b" stroke-width="1.6"%s marker-end="url(#a)"/>`, x1, y1, x2, y2, dash)
	}
	for _, id := range []string{"words", "facts", "turns", "episodes", "sleep", "lessons", "runs", "proposals", "yes", "skills", "search", "summaries", "cli"} {
		bx := boxes[id]
		fill, stroke, ink := "#eef2ff", "#6366f1", "#1e1b4b"
		if bx.muted {
			fill, stroke, ink = "#f1f5f9", "#94a3b8", "#64748b"
		}
		fmt.Fprintf(&b, `<g data-node="%s"><rect x="%d" y="%d" width="%d" height="%d" rx="10" fill="%s" stroke="%s"/>`, id, bx.x(), bx.y(), boxW, boxH, fill, stroke)
		title, ts := fitLines(bx.title, 14, 12, 2, true)
		lines, ls := fitLines(bx.line, 12, 10, 3-len(title), false)
		// The lines stand in the middle of the box, the title above the rest.
		height := float64(len(title))*(ts+3) + 4 + float64(len(lines))*(ls+3)
		y := float64(bx.y()) + (boxH-height)/2
		for _, l := range title {
			y += ts + 3
			fmt.Fprintf(&b, `<text x="%d" y="%.0f" font-size="%g" font-weight="600" fill="%s" data-w="%.0f">%s</text>`,
				bx.x()+12, y-3, ts, ink, textWidth(l, ts, true), html.EscapeString(l))
		}
		y += 4
		for _, l := range lines {
			y += ls + 3
			fmt.Fprintf(&b, `<text x="%d" y="%.0f" font-size="%g" fill="%s" data-w="%.0f">%s</text>`,
				bx.x()+12, y-3, ls, ink, textWidth(l, ls, false), html.EscapeString(l))
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// textWidth is a generous guess of how wide s is drawn at a font size, by its letters: no font is at hand to measure.
func textWidth(s string, size float64, bold bool) float64 {
	em := 0.0
	for _, r := range s {
		switch {
		case r == ' ':
			em += 0.3
		case strings.ContainsRune(".,:;'!|il«»()", r):
			em += 0.34
		case r >= '0' && r <= '9':
			em += 0.58
		case unicode.IsUpper(r):
			em += 0.72
		case r >= 'a' && r <= 'z':
			em += 0.56
		default:
			em += 0.62
		}
	}
	if bold {
		em *= 1.07
	}
	return em * size
}

// fitLines lays s into one line of a box at most a size smaller, else into at most n lines at the largest size down to
// least that holds it, by words; what still does not fit ends in an ellipsis.
func fitLines(s string, size, least float64, n int, bold bool) ([]string, float64) {
	// One line a size smaller reads better than two.
	for at := size; at >= max(least, size-1); at -= 0.5 {
		if lines, ok := wrap(s, at, 1, bold); ok {
			return lines, at
		}
	}
	for at := size; at >= least; at -= 0.5 {
		if lines, ok := wrap(s, at, n, bold); ok {
			return lines, at
		}
	}
	lines, _ := wrap(s, least, n, bold)
	last := []rune(lines[len(lines)-1])
	for len(last) > 1 && textWidth(string(last)+"…", least, bold) > textW {
		last = last[:len(last)-1]
	}
	if string(last) != lines[len(lines)-1] {
		lines[len(lines)-1] = strings.TrimRight(string(last), " ,") + "…"
	}
	return lines, least
}

// wrap breaks s into lines of words that fit a box at a font size; ok when n lines hold all of it.
func wrap(s string, size float64, n int, bold bool) ([]string, bool) {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		next := strings.TrimSpace(cur + " " + w)
		if cur != "" && textWidth(next, size, bold) > textW {
			lines, cur = append(lines, cur), w
			continue
		}
		cur = next
	}
	lines = append(lines, cur)
	ok := len(lines) <= n
	for _, l := range lines {
		ok = ok && textWidth(l, size, bold) <= textW
	}
	if len(lines) > n {
		lines = append(lines[:n-1], strings.Join(lines[n-1:], " "))
	}
	return lines, ok
}

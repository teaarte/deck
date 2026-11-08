package documents

import (
	"fmt"
	"html"
	"strings"

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
	boxW, boxH   = 204, 64
	padX, padY   = 18, 20
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
		"skills":    {3, 2, tr("Skills"), node["skills"].Text, false},
		"search":    {0, 3, "memory_search", tr("facts, lessons, skills, summaries, episodes"), false},
		"summaries": {1, 3, tr("Summaries of topics"), node["summaries"].Text, false},
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
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="14" font-weight="600" fill="%s">%s</text>`, bx.x()+12, bx.y()+26, ink, html.EscapeString(fit(bx.title, 26)))
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text></g>`, bx.x()+12, bx.y()+46, ink, html.EscapeString(fit(bx.line, 32)))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// fit is s cut to n letters with an ellipsis.
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

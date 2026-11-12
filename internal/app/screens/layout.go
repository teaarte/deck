package screens

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Today is the id of the card of Today in a layout.
const Today = "today"

// card is where a card of the tiles stands, in cells: Today or a project by its name.
type card struct {
	ID string `json:"id"`
	X  int64  `json:"x"`
	Y  int64  `json:"y"`
	W  int64  `json:"w"`
	H  int64  `json:"h"`
	// In is the folder a card of a module stands in instead of the grid.
	In string `json:"in,omitempty"`
}

// folderOf is the name of a folder a card id names, folder:<name>; empty for any other card.
func folderOf(id string) string {
	name, _ := strings.CutPrefix(id, folderPrefix)
	if name == id {
		return ""
	}
	return name
}

const folderPrefix = "folder:"

// storedCard is a card as the base keeps it: a project id, 0 for Today, or a folder by its name with the projects
// whose cards stand in it.
type storedCard struct {
	ID     int64  `json:"id"`
	X      int64  `json:"x"`
	Y      int64  `json:"y"`
	W      int64  `json:"w"`
	H      int64  `json:"h"`
	Folder string `json:"folder,omitempty"`
	In     string `json:"in,omitempty"`
}

type layoutGetIn struct {
	Columns int64 `desc:"How many columns of cells the grid has now"`
}

// deviceOf names the device of the call in the layouts; a call of no device has none of its own.
func deviceOf(act registry.Actor) string {
	if act.Device == 0 {
		return ""
	}
	return strconv.FormatInt(act.Device, 10)
}

// layoutView is the layout a device shows: its own for the columns, else one it is derived from. Columns is that of
// the cards, 0 when they give the order alone; Own says the device placed them itself.
type layoutView struct {
	Columns int64  `json:"columns"`
	Own     bool   `json:"own"`
	Cards   []card `json:"cards"`
	// Save is the button that keeps the cards the device placed, given as cards.
	Save map[string]any `json:"save"`
}

// layout is the layout of a device for its columns: its own, else its own of the nearest wider grid, then of the
// nearest narrower one, else the newest of any device for the columns, else the order of its own or the newest.
func (s *Service) layout(ctx context.Context, act registry.Actor, in layoutGetIn) (layoutView, error) {
	me := deviceOf(act)
	out := layoutView{Cards: []card{}, Save: map[string]any{"label": "Arrange the tiles", "command": registry.Deck("layout_set"),
		"params": map[string]any{"columns": in.Columns}, "input": "cards"}}
	ls, err := s.St.Q.ListLayouts(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	names, err := s.projectNames(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	pick := func(keep func(db.Layout) bool) *db.Layout {
		for i := range ls {
			if keep(ls[i]) {
				return &ls[i]
			}
		}
		return nil
	}
	l := pick(func(l db.Layout) bool { return l.Device == me && l.Columns == in.Columns })
	if l == nil {
		l = nearest(ls, me, in.Columns, true)
	}
	if l == nil {
		l = nearest(ls, me, in.Columns, false)
	}
	if l == nil {
		l = pick(func(l db.Layout) bool { return l.Columns == in.Columns })
	}
	order := false
	if l == nil {
		if l = pick(func(l db.Layout) bool { return l.Device == me }); l == nil {
			l = pick(func(l db.Layout) bool { return true })
		}
		order = true
	}
	if l == nil {
		return out, nil
	}
	out.Columns, out.Own = l.Columns, l.Device == me && !order
	for _, c := range stored(l.Cards) {
		if c.Folder != "" {
			out.Cards = append(out.Cards, card{ID: folderPrefix + c.Folder, X: c.X, Y: c.Y, W: c.W, H: c.H})
			continue
		}
		name, ok := names[c.ID]
		if !ok {
			continue
		}
		out.Cards = append(out.Cards, card{ID: name, X: c.X, Y: c.Y, W: c.W, H: c.H, In: c.In})
	}
	// A grid of one column is the order with the heights: its cards go across a wider grid as they went across it.
	if !order && l.Columns == 1 && in.Columns > 1 {
		for i := range out.Cards {
			if out.Cards[i].In == "" {
				out.Cards[i].X, out.Cards[i].W = 0, in.Columns
			}
		}
		out.Columns = in.Columns
	}
	if order || l.Columns == 0 {
		slices.SortStableFunc(out.Cards, func(a, b card) int { return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X)) })
		out.Columns = 0
	}
	return out, nil
}

type layoutSetIn struct {
	Columns int64  `desc:"How many columns of cells the grid has"`
	Cards   string `type:"json" desc:"The cards as a JSON list of {id, x, y, w, h} in cells: today, the name of a project, or folder:<name> a folder of cards; a card of a project with in, the name of a folder, stands in that folder"`
}

// layoutSet keeps where the cards of a device stand for its columns; a card of no project is dropped.
func (s *Service) layoutSet(ctx context.Context, act registry.Actor, in layoutSetIn) (any, error) {
	var cards []card
	if err := json.Unmarshal([]byte(in.Cards), &cards); err != nil {
		return nil, registry.Invalid("cards are a JSON list of {id, x, y, w, h}: %v", err)
	}
	me := deviceOf(act)
	if me == "" {
		return nil, registry.Refuse("the tiles are placed on a device of the owner, and this call comes from none")
	}
	if in.Columns < 1 || in.Columns > 24 {
		return nil, registry.Invalid("a layout is of 1 to 24 columns")
	}
	names, err := s.projectNames(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for id, name := range names {
		ids[name] = id
	}
	paused, err := s.pausedCards(ctx, act.Workspace, me, in.Columns, cards)
	if err != nil {
		return nil, err
	}
	keep := []storedCard{}
	seen := map[string]bool{}
	folders := map[string]bool{}
	for i, c := range cards {
		cards[i].In = strings.TrimSpace(c.In)
		if f := folderOf(c.ID); f != "" {
			name := strings.TrimSpace(f)
			if name == "" || strings.ContainsFunc(name, unicode.IsControl) {
				return nil, registry.Invalid("a folder is named by words, not %q", f)
			}
			cards[i].ID, folders[name] = folderPrefix+name, true
		}
	}
	// A folder holds on while a card in it is of a project paused, which the tiles do not show.
	held := map[string]bool{}
	for _, c := range cards {
		if c.In != "" && folders[c.In] && ids[c.ID] != 0 {
			held[c.In] = true
		}
	}
	for _, c := range paused {
		if c.In != "" && folders[c.In] {
			held[c.In] = true
		}
	}
	for _, c := range cards {
		id, ok := ids[c.ID]
		folder := folderOf(c.ID)
		switch {
		case folder != "" && !held[folder]:
			// A folder without a card in it goes.
			continue
		case folder != "" && utf8.RuneCountInString(folder) > 40:
			return nil, registry.Invalid("a folder is named in 40 characters at most: %q", folder)
		case folder == "" && !ok:
			continue
		case seen[c.ID]:
			return nil, registry.Invalid("card %s is placed twice", c.ID)
		case c.In != "" && (!folders[c.In] || id == 0):
			return nil, registry.Invalid("card %s stands in folder %q, which the cards do not have", c.ID, c.In)
		case c.In == "" && (c.W < 1 || c.H < 1 || c.X < 0 || c.Y < 0 || c.X+c.W > in.Columns):
			return nil, registry.Invalid("card %s does not fit %d columns", c.ID, in.Columns)
		case c.ID == Today && c.W < min(2, in.Columns):
			return nil, registry.Invalid("Today takes two cells of a row at least")
		}
		seen[c.ID] = true
		switch {
		case folder != "":
			keep = append(keep, storedCard{X: c.X, Y: c.Y, W: c.W, H: c.H, Folder: folder})
		case c.In != "":
			keep = append(keep, storedCard{ID: id, In: c.In})
		default:
			keep = append(keep, storedCard{ID: id, X: c.X, Y: c.Y, W: c.W, H: c.H})
		}
	}
	for _, c := range paused {
		if c.In != "" && !held[c.In] {
			continue
		}
		if c.In != "" || !slices.ContainsFunc(keep, func(k storedCard) bool { return k.In == "" && overlap(k, c) }) {
			keep = append(keep, c)
		}
	}
	raw, _ := json.Marshal(keep)
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetLayout(ctx, db.SetLayoutParams{WorkspaceID: act.Workspace, Device: me, Columns: in.Columns, Cards: string(raw),
			UpdatedAt: store.Time(s.Now())})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "cards": len(keep)}, nil
}

// projectNames are the names of the projects of a workspace a card stands for by id, Today under 0.
func (s *Service) projectNames(ctx context.Context, workspace int64) (map[int64]string, error) {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	out := map[int64]string{0: Today}
	for _, p := range ps {
		if p.Builtin == 0 {
			out[p.ID] = p.Name
		}
	}
	return out, err
}

// order is the order of the projects by the newest layout of any device, in reading order; none without one.
func (s *Service) order(ctx context.Context, q *db.Queries, workspace int64) []int64 {
	ls, err := q.ListLayouts(ctx, workspace)
	if err != nil || len(ls) == 0 {
		return nil
	}
	cs := stored(ls[0].Cards)
	// The modules in folders come after those of the grid, in the order they were put there.
	slices.SortStableFunc(cs, func(a, b storedCard) int {
		return cmp.Or(cmp.Compare(boolRank(a.In != ""), boolRank(b.In != "")), cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	out := make([]int64, 0, len(cs))
	for _, c := range cs {
		if c.Folder == "" {
			out = append(out, c.ID)
		}
	}
	return out
}

func stored(raw string) []storedCard {
	var cs []storedCard
	if err := json.Unmarshal([]byte(raw), &cs); err != nil {
		slog.Error("layout", "err", err)
	}
	return cs
}

// pausedCards are the cards of paused projects the layout of a device kept, which the tiles do not show and a save
// leaves where they stood for their resume.
func (s *Service) pausedCards(ctx context.Context, workspace int64, device string, columns int64, given []card) ([]storedCard, error) {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	ls, err := s.St.Q.ListLayouts(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var out []storedCard
	for _, l := range ls {
		if l.Device != device || l.Columns != columns {
			continue
		}
		for _, c := range stored(l.Cards) {
			i := slices.IndexFunc(ps, func(p db.Project) bool { return p.ID == c.ID })
			if i >= 0 && ps[i].Paused == 1 && !slices.ContainsFunc(given, func(g card) bool { return g.ID == ps[i].Name }) {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func overlap(a, b storedCard) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// nearest is the layout of a device for the grid nearest to columns, of more of them or of fewer; nil without one.
func nearest(ls []db.Layout, device string, columns int64, wider bool) *db.Layout {
	var best *db.Layout
	for i := range ls {
		x := &ls[i]
		switch {
		case x.Device != device || x.Columns == 0:
		case wider && x.Columns > columns && (best == nil || x.Columns < best.Columns):
			best = x
		case !wider && x.Columns < columns && (best == nil || x.Columns > best.Columns):
			best = x
		}
	}
	return best
}

func (s *Service) layoutCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("layout_get", "Layout of the tiles",
			"Gives where the cards of the tiles stand on a device for its columns: its own, else the one they are derived from; columns 0 gives the order alone.",
			registry.OwnerSide, s.layout).On(events.Settings, events.Projects),
		registry.Def("layout_set", "Arrange the tiles",
			"Keeps where the cards of the tiles stand on a device for a number of columns, in cells.",
			registry.OwnerSide, s.layoutSet).Stays(),
	}
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

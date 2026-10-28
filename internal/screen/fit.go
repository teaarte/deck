package screen

import "deck2/internal/manifest"

// A widget is laid in thirds of a line, the same in the client: a line of a list takes three, the title of a block
// one, a number three and a chart at least three; the client leaves out a block the card has no room for.
const (
	thirdsTitle = 1
	thirdsLine  = 3
	thirdsStat  = 3
	thirdsChart = 3
)

// Thirds is what a card of h rows of cells holds under its head: three lines a row, less one for the head.
func Thirds(h int) int { return 9*h - 3 }

// least is what a block of a widget takes at the fewest: a list one line under its title.
func least(b manifest.Block) int {
	switch b.Type {
	case "stat":
		return thirdsStat
	case "chart":
		return titled(b) + thirdsChart
	}
	return titled(b) + thirdsLine
}

// titled is what the title of a block takes.
func titled(b manifest.Block) int {
	if b.Title != "" {
		return thirdsTitle
	}
	return 0
}

// took is what a block of a widget took once drawn: a list its title and a line an item, or one for its empty or
// failed state.
func (r *renderer) took(id string, b manifest.Block) int {
	if b.Type != "list" {
		return least(b)
	}
	return titled(b) + thirdsLine*max(1, len(r.read[id].items))
}

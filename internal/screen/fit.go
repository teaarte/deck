package screen

import "deck2/internal/manifest"

// widget are the thirds of a line a widget is laid in.
var widget = manifest.Thirds

// Thirds is what a card of h rows of cells holds under its head.
func Thirds(h int) int { return widget.Row*h - widget.Head }

// least is what a block of a widget takes at the fewest: a list one line under its title.
func least(b manifest.Block) int {
	k := kindOf(b.Type)
	if k.inside {
		return k.least
	}
	return titled(b) + k.least
}

// titled is what the title of a block takes.
func titled(b manifest.Block) int {
	if b.Title != "" {
		return widget.Title
	}
	return 0
}

// took is what a block of a widget took once drawn: a list its title and a line an item, or one for its empty or
// failed state.
func (r *renderer) took(id string, b manifest.Block) int {
	if !kindOf(b.Type).lined {
		return least(b)
	}
	return titled(b) + widget.Line*max(1, len(r.read[id].items))
}

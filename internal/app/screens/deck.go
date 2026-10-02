package screens

import (
	"context"
)

// Deck is what the screen sources read from the rest of deck.
type Deck interface {
	Waits(ctx context.Context, project int64) (map[int64][]int64, error)
	Chat(ctx context.Context, workspace int64, topic string) (any, error)
}

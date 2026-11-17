// Package txguard marks a context as inside a transaction of the store and checks the doors that must not open there
// (the network, a box, a provider's turn): a test panics, deck logs. A wait inside a Tx it does not see.
package txguard

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"testing"
)

type key struct{}

// Within marks ctx as inside a transaction until end is called.
func Within(ctx context.Context) (in context.Context, end func()) {
	open := new(atomic.Bool)
	open.Store(true)
	return context.WithValue(ctx, key{}, open), func() { open.Store(false) }
}

// In reports whether ctx is of a transaction that is still open.
func In(ctx context.Context) bool {
	open, _ := ctx.Value(key{}).(*atomic.Bool)
	return open != nil && open.Load()
}

// Check stops a door opened inside a transaction: it panics in a test binary and logs the door with its stack in
// deck. A context not made from the transaction's, such as context.Background, passes unseen.
func Check(ctx context.Context, door string) {
	if !In(ctx) {
		return
	}
	if testing.Testing() {
		panic("txguard: " + door + " inside a transaction of the store")
	}
	slog.Error("txguard: "+door+" inside a transaction of the store; the writer waits for it", "stack", string(debug.Stack()))
}

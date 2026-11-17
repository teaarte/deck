package registry

import (
	"context"
	"sync"
)

// once is what one read of deck computed once, kept for the reads it makes in turn; it ends with the read.
type once struct {
	mu sync.Mutex
	v  map[any]any
}

type onceKey struct{}

// inRead gives ctx the memory of a read: its own, or that of the read that calls it.
func inRead(ctx context.Context) context.Context {
	if _, ok := ctx.Value(onceKey{}).(*once); ok {
		return ctx
	}
	return context.WithValue(ctx, onceKey{}, &once{v: map[any]any{}})
}

// Once is the value of key in the read ctx belongs to, made by make the first time; outside a read, made anew.
func Once[T any](ctx context.Context, key any, make func() T) T {
	o, ok := ctx.Value(onceKey{}).(*once)
	if !ok {
		return make()
	}
	o.mu.Lock()
	v, ok := o.v[key]
	o.mu.Unlock()
	if ok {
		return v.(T)
	}
	made := make()
	o.mu.Lock()
	defer o.mu.Unlock()
	if v, ok := o.v[key]; ok {
		return v.(T)
	}
	o.v[key] = made
	return made
}

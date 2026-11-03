package auth

import (
	"context"
	"sync"
)

// live are the open connections of devices, such as the stream of events, which a revoke ends.
type live struct {
	mu   sync.Mutex
	next int
	of   map[int64]map[int]context.CancelFunc
}

// Hold ties a connection of a device to it: the context ends when the device is revoked; release lets it go.
func (a *Auth) Hold(ctx context.Context, device int64) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if device == 0 {
		return ctx, cancel
	}
	l := &a.live
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.of == nil {
		l.of = map[int64]map[int]context.CancelFunc{}
	}
	if l.of[device] == nil {
		l.of[device] = map[int]context.CancelFunc{}
	}
	l.next++
	id := l.next
	l.of[device][id] = cancel
	return ctx, func() {
		cancel()
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.of[device], id)
	}
}

// Kick ends the open connections of a device; deck calls it after the revoke is committed.
func (a *Auth) Kick(device int64) {
	l := &a.live
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, cancel := range l.of[device] {
		cancel()
	}
	delete(l.of, device)
}

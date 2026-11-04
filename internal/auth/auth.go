// Package auth signs the owner's devices in: codes that pair a device, passkeys by origin, a device for each browser,
// the sessions of their cookies, the limits of tries by address and the live connections a revoked device loses.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"deck2/internal/home"
	"deck2/internal/nonce"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Kinds of devices.
const (
	Browser = "browser"
	Node    = "node"
)

// Times of a session: it lasts a day after its last request, a week at most.
const (
	Idle    = 24 * time.Hour
	Longest = 7 * 24 * time.Hour
	// touchEvery is how stale the last request of a session may be before a request writes it again.
	touchEvery = time.Minute
)

type Config struct {
	Now  func() time.Time
	Home home.Layout
	// Origins are the addresses of deck; passkeys belong to their names.
	Origins *Origins
}

type Deps struct {
	Store *store.Store
}

// Auth signs devices in; its codes are files, its ceremonies and live connections in memory.
type Auth struct {
	cfg Config
	st  *store.Store
	mu  sync.Mutex
	// ceremonies are the passkey ceremonies begun and not finished, by their id.
	ceremonies map[string]ceremony
	live       live
	// Tries limits the requests of signing in by address; Misses the refused tokens.
	Tries, Misses *Limiter
}

func New(cfg Config, deps Deps) *Auth {
	return &Auth{cfg: cfg, st: deps.Store, ceremonies: map[string]ceremony{},
		Tries:  NewLimiter(30, time.Minute, cfg.Now),
		Misses: NewLimiter(10, 10*time.Minute, cfg.Now)}
}

// Origins are the addresses of deck.
func (a *Auth) Origins() *Origins { return a.cfg.Origins }

// Principal is the device a session signs in.
type Principal struct {
	Device    int64
	Workspace int64
	Kind      string
	// Unsettled marks a session of before devices were browsers, which Settle puts on its browser's device.
	Unsettled bool
}

// Actor is the owner on the device.
func (p Principal) Actor() registry.Actor {
	return registry.Actor{Kind: registry.Owner, Workspace: p.Workspace, Device: p.Device}
}

// ErrSignIn is a refusal of a code, a ceremony or a passkey; it says no more of why to the caller.
var ErrSignIn = errors.New("deck: the sign-in did not go through; ask for a new link")

func hash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// start begins a session of a device and returns its cookie; it runs in the transaction that made the device.
func (a *Auth) start(ctx context.Context, q *db.Queries, device int64) (string, error) {
	now := a.cfg.Now()
	if err := q.DeleteSessionsBefore(ctx, db.DeleteSessionsBeforeParams{Seen: store.Time(now.Add(-Idle)), Created: store.Time(now.Add(-Longest))}); err != nil {
		return "", err
	}
	raw := nonce.Token()
	return raw, q.CreateSession(ctx, db.CreateSessionParams{Hash: hash(raw), DeviceID: device, CreatedAt: store.Time(now), SeenAt: store.Time(now)})
}

// Session is the device a cookie signs in while its session lasts; a request renews it.
func (a *Auth) Session(ctx context.Context, raw string) (Principal, bool) {
	if raw == "" {
		return Principal{}, false
	}
	row, err := a.st.Q.SessionByHash(ctx, hash(raw))
	if err != nil {
		return Principal{}, false
	}
	now := a.cfg.Now()
	seen, err1 := time.Parse(store.TimeLayout, row.SeenAt)
	made, err2 := time.Parse(store.TimeLayout, row.CreatedAt)
	if err1 != nil || err2 != nil || now.Sub(seen) > Idle || now.Sub(made) > Longest {
		_ = a.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteSession(ctx, hash(raw)) })
		return Principal{}, false
	}
	if now.Sub(seen) > touchEvery {
		_ = a.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return errors.Join(q.TouchSession(ctx, db.TouchSessionParams{SeenAt: store.Time(now), Hash: hash(raw)}),
				q.TouchDevice(ctx, db.TouchDeviceParams{SeenAt: store.At(now), ID: row.Device.ID}))
		})
	}
	return Principal{Device: row.Device.ID, Workspace: row.Device.WorkspaceID, Kind: row.Device.Kind, Unsettled: row.Settled == 0}, true
}

// Alive says the session of a cookie lasts, without renewing it: an open stream holds no session up.
func (a *Auth) Alive(ctx context.Context, raw string) bool {
	row, err := a.st.Q.SessionByHash(ctx, hash(raw))
	if err != nil {
		return false
	}
	now := a.cfg.Now()
	seen, err1 := time.Parse(store.TimeLayout, row.SeenAt)
	made, err2 := time.Parse(store.TimeLayout, row.CreatedAt)
	return err1 == nil && err2 == nil && now.Sub(seen) <= Idle && now.Sub(made) <= Longest
}

// Logout ends the session of a cookie.
func (a *Auth) Logout(ctx context.Context, raw string) error {
	return a.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteSession(ctx, hash(raw)) })
}

// Signed says a passkey or a node may sign in already, so deck needs no first link.
func (a *Auth) Signed(ctx context.Context) bool {
	n, err := a.st.Q.CountSigned(ctx)
	return err != nil || n > 0
}

package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"deck2/internal/nonce"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// MarkLife is how long a browser keeps the mark of its device: as long as a browser keeps a cookie.
const MarkLife = 400 * 24 * time.Hour

// Client is what a browser says of itself at a step of signing in: its User-Agent and the mark of its device, which
// it keeps in a cookie; the mark only names the device a new session goes to and signs nothing in.
type Client struct {
	Agent string
	Mark  string
}

// Named is the name of a browser's device by its User-Agent, such as "iPhone — Safari", until the owner renames it.
func Named(agent string) string {
	platform := "Browser"
	for _, p := range []struct{ name, in string }{{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"},
		{"ChromeOS", "CrOS"}, {"Mac", "Macintosh"}, {"Windows", "Windows"}, {"Linux", "Linux"}} {
		if strings.Contains(agent, p.in) {
			platform = p.name
			break
		}
	}
	for _, b := range []struct{ name, in string }{{"Edge", "Edg"}, {"Firefox", "Firefox/"}, {"Firefox", "FxiOS/"},
		{"Chrome", "Chrome/"}, {"Chrome", "CriOS/"}, {"Safari", "Safari/"}} {
		if strings.Contains(agent, b.in) {
			return platform + " — " + b.name
		}
	}
	return platform
}

// ErrUnknown refuses a passkey to a browser deck does not know as a device: a new one, a revoked one or one that lost
// its mark; such a browser pairs by a link.
var ErrUnknown = errors.New("deck: pair this device by a link from another device or by deck pair")

// known is the device a browser's mark names while it may still sign in, in the workspace.
func known(ctx context.Context, q *db.Queries, workspace int64, br Client) (db.Device, error) {
	if br.Mark == "" {
		return db.Device{}, ErrUnknown
	}
	d, err := q.DeviceByMark(ctx, marked(br.Mark))
	if errors.Is(err, sql.ErrNoRows) || err == nil && workspace != 0 && d.WorkspaceID != workspace {
		return db.Device{}, ErrUnknown
	}
	return d, err
}

// browser is the device of a browser signed in by a passkey: its own while its mark names one that may still sign
// in, else, if fresh, a new one named as given or by its User-Agent; it gives the mark the browser keeps.
func (a *Auth) browser(ctx context.Context, q *db.Queries, workspace, passkey int64, name string, br Client, fresh bool) (int64, string, error) {
	now := store.At(a.cfg.Now())
	key := sql.NullInt64{Int64: passkey, Valid: passkey != 0}
	d, err := known(ctx, q, workspace, br)
	if err == nil && d.PasskeyID != key {
		// A session of another passkey ends here, so revoking that passkey leaves none of it.
		err = q.DeleteDeviceSessions(ctx, d.ID)
	}
	if err == nil {
		return d.ID, br.Mark, q.SignDevice(ctx, db.SignDeviceParams{PasskeyID: key, Mark: d.Mark, SeenAt: now, ID: d.ID})
	}
	if !errors.Is(err, ErrUnknown) || !fresh {
		return 0, "", err
	}
	if name == "" {
		name = Named(br.Agent)
	}
	mark := nonce.Token()
	made, err := q.CreateDevice(ctx, db.CreateDeviceParams{WorkspaceID: workspace, Kind: Browser, Name: name, PasskeyID: key, Mark: marked(mark), SeenAt: now})
	return made.ID, mark, err
}

func marked(mark string) sql.NullString { return sql.NullString{String: hash(mark), Valid: true} }

// Settle puts a session of before devices were browsers on the device of its browser, once: the first browser of a
// device takes it, another gets a device of its own by the same passkey; it gives the mark the browser keeps.
func (a *Auth) Settle(ctx context.Context, raw string, br Client) (Principal, string, error) {
	type settled struct {
		p    Principal
		mark string
	}
	s, err := store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (settled, error) {
		row, err := q.SessionByHash(ctx, hash(raw))
		if err != nil {
			return settled{}, err
		}
		d := row.Device
		p := Principal{Device: d.ID, Workspace: d.WorkspaceID, Kind: d.Kind}
		if row.Settled != 0 {
			return settled{p: p}, nil
		}
		mark := br.Mark
		switch {
		case !d.Mark.Valid && mark == "":
			mark = nonce.Token()
			err = q.SignDevice(ctx, db.SignDeviceParams{PasskeyID: d.PasskeyID, Mark: marked(mark), SeenAt: d.SeenAt, ID: d.ID})
		case !d.Mark.Valid || d.Mark.String != hash(mark):
			// Which browser of the two the pushes of the device went to is unknown: each turns them on again.
			if _, err = q.DeletePush(ctx, db.DeletePushParams{WorkspaceID: d.WorkspaceID, ID: d.ID}); err == nil {
				p.Device, mark, err = a.browser(ctx, q, d.WorkspaceID, d.PasskeyID.Int64, "", br, true)
			}
		}
		if err != nil {
			return settled{}, err
		}
		return settled{p: p, mark: mark}, q.SettleSession(ctx, db.SettleSessionParams{DeviceID: p.Device, Hash: hash(raw)})
	})
	return s.p, s.mark, err
}

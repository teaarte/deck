package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"deck2/internal/nonce"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// ceremonyLife is how long a passkey ceremony waits for the browser.
const ceremonyLife = 5 * time.Minute

// ceremony is a passkey ceremony begun at an origin: of pairing by a code under a new user handle, or of signing in.
type ceremony struct {
	session webauthn.SessionData
	origin  string
	code    string
	handle  []byte
	until   time.Time
}

// Begun is what a begun ceremony gives the browser, or the session of a device signed in: a browser's in its cookie,
// that of the launcher or a node as a key it sends in the header Authorization.
type Begun struct {
	Ceremony string `json:"ceremony,omitempty"`
	Options  any    `json:"options,omitempty"`
	Cookie   string `json:"-"`
	Key      string `json:"key,omitempty"`
	Device   int64  `json:"device,omitempty"`
	// Kicked are the devices the pairing took the place of, whose open connections end once it is committed.
	Kicked []int64 `json:"-"`
}

// owner is the owner as a device knows them: a user handle of the device's own, so the passkey of one device never
// takes the place of another's in an authenticator, with that device's passkey.
type owner struct {
	handle []byte
	creds  []webauthn.Credential
}

func (o owner) WebAuthnID() []byte                         { return o.handle }
func (o owner) WebAuthnName() string                       { return "owner" }
func (o owner) WebAuthnDisplayName() string                { return "deck" }
func (o owner) WebAuthnCredentials() []webauthn.Credential { return o.creds }

func relying(o Origin) (*webauthn.WebAuthn, error) {
	if !o.Passkeys() {
		return nil, errors.New("deck: a browser signs in to deck only over https by a name; open deck at its https address")
	}
	return webauthn.New(&webauthn.Config{RPID: o.RPID(), RPDisplayName: "deck", RPOrigins: []string{o.Main}})
}

// keep holds a ceremony until it is finished or its time is out.
func (a *Auth) keep(c ceremony) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.cfg.Now()
	for k, x := range a.ceremonies {
		if now.After(x.until) {
			delete(a.ceremonies, k)
		}
	}
	id := nonce.Token()
	c.until = now.Add(ceremonyLife)
	a.ceremonies[id] = c
	return id
}

// finish takes a ceremony out once; a second finish of it finds none.
func (a *Auth) finish(id string, o Origin) (ceremony, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.ceremonies[id]
	delete(a.ceremonies, id)
	if !ok || c.origin != o.Main || a.cfg.Now().After(c.until) {
		return ceremony{}, ErrSignIn
	}
	return c, nil
}

// PairBegin begins to pair a device by its code at an origin: a passkey ceremony for a browser; the launcher, on this
// machine only, and a node pair at once with no passkey and get a key. A launcher takes the place of the one before.
func (a *Auth) PairBegin(ctx context.Context, o Origin, raw string, loopback bool) (Begun, error) {
	a.mu.Lock()
	c, err := a.take(raw, true)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	if c.Kind != Browser {
		if c.Kind == Launcher && !loopback {
			return Begun{}, errors.New("deck: the launcher pairs only on the machine deck runs on")
		}
		a.mu.Lock()
		err := a.spend(c)
		a.mu.Unlock()
		if err != nil {
			return Begun{}, err
		}
		return a.device(ctx, db.CreateDeviceParams{WorkspaceID: c.Workspace, Kind: c.Kind, Name: c.Name})
	}
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	handle := []byte(nonce.Token())
	creation, session, err := w.BeginRegistration(owner{handle: handle},
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(), UserVerification: protocol.VerificationPreferred}))
	if err != nil {
		return Begun{}, err
	}
	return Begun{Ceremony: a.keep(ceremony{session: *session, origin: o.Main, code: raw, handle: handle}), Options: creation}, nil
}

// PairFinish makes the device of a pairing ceremony with the passkey the browser made, named as the browser says, and
// signs it in; the code is spent.
func (a *Auth) PairFinish(ctx context.Context, o Origin, id, name string, body []byte) (Begun, error) {
	cer, err := a.finish(id, o)
	if err != nil {
		return Begun{}, err
	}
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body))
	if err != nil {
		return Begun{}, ErrSignIn
	}
	a.mu.Lock()
	c, err := a.take(cer.code, false)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	cred, err := w.CreateCredential(owner{handle: cer.handle}, cer.session, parsed)
	if err != nil {
		return Begun{}, ErrSignIn
	}
	a.mu.Lock()
	err = a.spend(c)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	if name == "" {
		name = c.Name
	}
	if name == "" {
		name = "a browser"
	}
	b, _ := json.Marshal(cred)
	return a.device(ctx, db.CreateDeviceParams{WorkspaceID: c.Workspace, Kind: Browser, Name: name, RpID: o.RPID(), CredentialID: cred.ID,
		UserHandle: cer.handle, Credential: string(b)})
}

// device makes a device and its first session; a code of no workspace pairs it in deck's first one.
func (a *Auth) device(ctx context.Context, p db.CreateDeviceParams) (Begun, error) {
	now := a.cfg.Now()
	p.SeenAt = store.At(now)
	b, err := store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		if p.WorkspaceID == 0 {
			spaces, err := q.ListWorkspaces(ctx)
			if err != nil || len(spaces) == 0 {
				return Begun{}, errors.Join(err, ErrSignIn)
			}
			p.WorkspaceID = spaces[0]
		}
		var b Begun
		if p.Kind == Launcher {
			if err := q.DeleteKindSessions(ctx, db.DeleteKindSessionsParams{WorkspaceID: p.WorkspaceID, Kind: Launcher}); err != nil {
				return Begun{}, err
			}
			gone, err := q.DeleteKind(ctx, db.DeleteKindParams{WorkspaceID: p.WorkspaceID, Kind: Launcher})
			if err != nil {
				return Begun{}, err
			}
			b.Kicked = gone
		}
		d, err := q.CreateDevice(ctx, p)
		if err != nil {
			return Begun{}, err
		}
		raw, err := a.start(ctx, q, d.ID)
		b.Device = d.ID
		if p.Kind == Browser {
			b.Cookie = raw
		} else {
			b.Key = raw
		}
		return b, err
	})
	for _, id := range b.Kicked {
		a.Kick(id)
	}
	return b, err
}

// LoginBegin begins to sign in with a passkey of the origin; the browser offers those it has.
func (a *Auth) LoginBegin(o Origin) (Begun, error) {
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	assertion, session, err := w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return Begun{}, err
	}
	return Begun{Ceremony: a.keep(ceremony{session: *session, origin: o.Main}), Options: assertion}, nil
}

// LoginFinish signs in the device whose passkey answered the ceremony.
func (a *Auth) LoginFinish(ctx context.Context, o Origin, id string, body []byte) (Begun, error) {
	cer, err := a.finish(id, o)
	if err != nil {
		return Begun{}, err
	}
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body))
	if err != nil {
		return Begun{}, ErrSignIn
	}
	var dev db.Device
	_, cred, err := w.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		d, err := a.st.Q.DeviceByCredential(ctx, db.DeviceByCredentialParams{CredentialID: rawID, UserHandle: handle, RpID: o.RPID()})
		if err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(d.Credential), &c); err != nil {
			return nil, err
		}
		dev = d
		return owner{handle: d.UserHandle, creds: []webauthn.Credential{c}}, nil
	}, cer.session, parsed)
	if err != nil {
		return Begun{}, ErrSignIn
	}
	b, _ := json.Marshal(cred)
	return store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		if err := q.SetDeviceCredential(ctx, db.SetDeviceCredentialParams{Credential: string(b), SeenAt: store.At(a.cfg.Now()), ID: dev.ID}); err != nil {
			return Begun{}, err
		}
		cookie, err := a.start(ctx, q, dev.ID)
		return Begun{Cookie: cookie, Device: dev.ID}, err
	})
}

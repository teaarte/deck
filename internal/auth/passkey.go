package auth

import (
	"bytes"
	"cmp"
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

// ceremony is a passkey ceremony begun at an origin: of pairing by a code under a new user handle or by a passkey the
// browser holds already, or of signing in.
type ceremony struct {
	session webauthn.SessionData
	origin  string
	code    string
	handle  []byte
	held    bool
	until   time.Time
}

// Begun is what a begun ceremony gives the browser, or the session of a device signed in: a browser's in its cookie
// with the mark of its device, that of a node as a key it sends in the header Authorization.
type Begun struct {
	Ceremony string `json:"ceremony,omitempty"`
	Options  any    `json:"options,omitempty"`
	Cookie   string `json:"-"`
	Mark     string `json:"-"`
	Key      string `json:"key,omitempty"`
	Device   int64  `json:"device,omitempty"`
}

// owner is the owner as a passkey knows them: a user handle of the passkey's own, so one passkey never takes the place
// of another in an authenticator.
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

// PairBegin begins to pair a device by its code: a ceremony that makes a passkey deck has not, or, held, asks for one
// the browser holds; a node pairs at once with no passkey and gets a key.
func (a *Auth) PairBegin(ctx context.Context, o Origin, raw string, held bool) (Begun, error) {
	// The ceremony of a passkey held is the second half of the same try.
	a.mu.Lock()
	c, err := a.take(raw, !held)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	if c.Kind != Browser {
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
	space, err := workspace(ctx, a.st.Q, c.Workspace)
	if err != nil {
		return Begun{}, err
	}
	ids, err := a.st.Q.PasskeyCredentials(ctx, db.PasskeyCredentialsParams{WorkspaceID: space, RpID: o.RPID()})
	if err != nil {
		return Begun{}, err
	}
	// A browser holding one of these refuses to make another; it pairs by the one it holds.
	live := make([]protocol.CredentialDescriptor, len(ids))
	for i, id := range ids {
		live[i] = protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id}
	}
	if held {
		assertion, session, err := w.BeginDiscoverableLogin(webauthn.WithAllowedCredentials(live),
			webauthn.WithUserVerification(protocol.VerificationPreferred))
		if err != nil {
			return Begun{}, err
		}
		return Begun{Ceremony: a.keep(ceremony{session: *session, origin: o.Main, code: raw, held: true}), Options: assertion}, nil
	}
	handle := []byte(nonce.Token())
	creation, session, err := w.BeginRegistration(owner{handle: handle}, webauthn.WithExclusions(live),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(), UserVerification: protocol.VerificationPreferred}))
	if err != nil {
		return Begun{}, err
	}
	return Begun{Ceremony: a.keep(ceremony{session: *session, origin: o.Main, code: raw, handle: handle}), Options: creation}, nil
}

// PairFinish keeps the passkey the browser made in a pairing ceremony, or takes the one it held, and signs the browser
// in by it, its device named as the browser says; the code is spent.
func (a *Auth) PairFinish(ctx context.Context, o Origin, id, name string, br Client, body []byte) (Begun, error) {
	cer, err := a.finish(id, o)
	if err != nil {
		return Begun{}, err
	}
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	if cer.held {
		return a.pairHeld(ctx, o, w, cer, name, br, body)
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
	b, _ := json.Marshal(cred)
	return store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		space, err := workspace(ctx, q, c.Workspace)
		if err != nil {
			return Begun{}, err
		}
		key, err := q.CreatePasskey(ctx, db.CreatePasskeyParams{WorkspaceID: space, Name: cmp.Or(name, Named(br.Agent)), RpID: o.RPID(),
			CredentialID: cred.ID, UserHandle: cer.handle, Credential: string(b), SeenAt: store.At(a.cfg.Now())})
		if err != nil {
			return Begun{}, err
		}
		return a.signIn(ctx, q, space, key.ID, name, br, true)
	})
}

// pairHeld pairs a browser by a passkey of deck it holds already, as one synced to it: a new device of that passkey.
func (a *Auth) pairHeld(ctx context.Context, o Origin, w *webauthn.WebAuthn, cer ceremony, name string, br Client, body []byte) (Begun, error) {
	a.mu.Lock()
	c, err := a.take(cer.code, false)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	key, cred, err := a.assert(ctx, o, w, cer, body)
	if err != nil {
		return Begun{}, err
	}
	// A passkey of another workspace leaves the code for the right one.
	if space, err := workspace(ctx, a.st.Q, c.Workspace); err != nil || key.WorkspaceID != space {
		return Begun{}, errors.Join(err, ErrSignIn)
	}
	a.mu.Lock()
	err = a.spend(c)
	a.mu.Unlock()
	if err != nil {
		return Begun{}, err
	}
	return store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		space, err := workspace(ctx, q, c.Workspace)
		if err != nil {
			return Begun{}, err
		}
		if key.WorkspaceID != space {
			return Begun{}, ErrSignIn
		}
		if err := a.used(ctx, q, key, cred); err != nil {
			return Begun{}, err
		}
		return a.signIn(ctx, q, space, key.ID, cmp.Or(name, c.Name), br, true)
	})
}

// signIn starts a session of a browser by a passkey on the browser's device; a new device only if fresh.
func (a *Auth) signIn(ctx context.Context, q *db.Queries, space, passkey int64, name string, br Client, fresh bool) (Begun, error) {
	device, mark, err := a.browser(ctx, q, space, passkey, name, br, fresh)
	if err != nil {
		return Begun{}, err
	}
	cookie, err := a.start(ctx, q, device)
	return Begun{Cookie: cookie, Mark: mark, Device: device}, err
}

// workspace is that of a code, or deck's first one for a code of none.
func workspace(ctx context.Context, q *db.Queries, of int64) (int64, error) {
	if of != 0 {
		return of, nil
	}
	spaces, err := q.ListWorkspaces(ctx)
	if err != nil || len(spaces) == 0 {
		return 0, errors.Join(err, ErrSignIn)
	}
	return spaces[0], nil
}

// device makes a node and its first session, whose key it gives.
func (a *Auth) device(ctx context.Context, p db.CreateDeviceParams) (Begun, error) {
	p.SeenAt = store.At(a.cfg.Now())
	return store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		var err error
		if p.WorkspaceID, err = workspace(ctx, q, p.WorkspaceID); err != nil {
			return Begun{}, err
		}
		d, err := q.CreateDevice(ctx, p)
		if err != nil {
			return Begun{}, err
		}
		raw, err := a.start(ctx, q, d.ID)
		return Begun{Device: d.ID, Key: raw}, err
	})
}

// LoginBegin begins to sign in with a passkey of the origin; the browser offers those it has. Only a browser deck knows
// as a device signs in by a passkey; another pairs by a link.
func (a *Auth) LoginBegin(ctx context.Context, o Origin, br Client) (Begun, error) {
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	if _, err := known(ctx, a.st.Q, 0, br); err != nil {
		return Begun{}, err
	}
	assertion, session, err := w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		return Begun{}, err
	}
	return Begun{Ceremony: a.keep(ceremony{session: *session, origin: o.Main}), Options: assertion}, nil
}

// LoginFinish signs the browser in by the passkey that answered the ceremony, on the browser's device.
func (a *Auth) LoginFinish(ctx context.Context, o Origin, id string, br Client, body []byte) (Begun, error) {
	cer, err := a.finish(id, o)
	if err != nil || cer.code != "" {
		return Begun{}, ErrSignIn
	}
	w, err := relying(o)
	if err != nil {
		return Begun{}, err
	}
	key, cred, err := a.assert(ctx, o, w, cer, body)
	if err != nil {
		return Begun{}, err
	}
	return store.TxGet(a.st, ctx, func(ctx context.Context, q *db.Queries) (Begun, error) {
		if err := a.used(ctx, q, key, cred); err != nil {
			return Begun{}, err
		}
		return a.signIn(ctx, q, key.WorkspaceID, key.ID, "", br, false)
	})
}

// assert is the passkey of deck that answered a ceremony of signing in, with its credential as it is now.
func (a *Auth) assert(ctx context.Context, o Origin, w *webauthn.WebAuthn, cer ceremony, body []byte) (db.Passkey, string, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body))
	if err != nil {
		return db.Passkey{}, "", ErrSignIn
	}
	var key db.Passkey
	_, cred, err := w.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		k, err := a.st.Q.PasskeyByCredential(ctx, db.PasskeyByCredentialParams{CredentialID: rawID, UserHandle: handle, RpID: o.RPID()})
		if err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(k.Credential), &c); err != nil {
			return nil, err
		}
		key = k
		return owner{handle: k.UserHandle, creds: []webauthn.Credential{c}}, nil
	}, cer.session, parsed)
	if err != nil {
		return db.Passkey{}, "", ErrSignIn
	}
	b, _ := json.Marshal(cred)
	return key, string(b), nil
}

// used keeps the credential of a passkey that signed in, unless it was revoked since the ceremony read it.
func (a *Auth) used(ctx context.Context, q *db.Queries, key db.Passkey, cred string) error {
	if _, err := q.PasskeyByCredential(ctx, db.PasskeyByCredentialParams{CredentialID: key.CredentialID, UserHandle: key.UserHandle, RpID: key.RpID}); err != nil {
		return errors.Join(err, ErrSignIn)
	}
	return q.SetPasskeyCredential(ctx, db.SetPasskeyCredentialParams{Credential: cred, SeenAt: store.At(a.cfg.Now()), ID: key.ID})
}

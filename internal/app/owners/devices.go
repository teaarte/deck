package owners

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"rsc.io/qr"

	"deck2/internal/auth"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// deviceView is a device of the owner — a browser or a node — with the buttons of its row.
type deviceView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
	SeenAt    string `json:"seen_at,omitempty"`
	// This marks the device the call came from.
	This bool `json:"this"`
	// Passkey is the name of the passkey a browser signed in with last; a device of the pushes before pairing has none.
	Passkey   string `json:"passkey"`
	PasskeyID int64  `json:"passkey_id,omitempty"`
	Push      bool   `json:"push"`
	Revoked   bool   `json:"revoked"`
	// Revoke is the button that signs the device out for good; StopPush the one that stops its pushes.
	Revoke   map[string]any `json:"revoke,omitempty"`
	StopPush map[string]any `json:"stop_push,omitempty"`
}

func (s *Service) devicesList(ctx context.Context, act registry.Actor, _ struct{}) ([]deviceView, error) {
	ds, err := s.St.Q.ListDevices(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]deviceView, 0, len(ds))
	for _, row := range ds {
		d := row.Device
		v := deviceView{ID: d.ID, Name: d.Name, Kind: d.Kind, CreatedAt: d.CreatedAt, SeenAt: d.SeenAt.String, This: d.ID == act.Device,
			Passkey: row.PasskeyName, PasskeyID: d.PasskeyID.Int64, Push: d.PushEndpoint.Valid, Revoked: d.RevokedAt.Valid}
		if !v.Revoked {
			v.Revoke = map[string]any{"verb": "revoke", "command": registry.Deck("device_revoke"), "params": map[string]any{"device": d.ID}}
		}
		if v.Push {
			v.StopPush = map[string]any{"verb": "remove", "command": registry.Deck("push_unsubscribe"), "params": map[string]any{"device": d.ID}}
		}
		out = append(out, v)
	}
	return out, nil
}

type deviceRenameIn struct {
	Device int64  `desc:"Device id"`
	Name   string `desc:"New name of the device"`
}

func (s *Service) deviceRename(ctx context.Context, act registry.Actor, in deviceRenameIn) (any, error) {
	name := untrusted.Cut(strings.TrimSpace(in.Name), 80)
	if name == "" {
		return nil, registry.Invalid("a device needs a name")
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RenameDevice(ctx, db.RenameDeviceParams{Name: name, ID: in.Device, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no device %d", in.Device)
	}
	return map[string]any{"device": in.Device, "name": name}, nil
}

type deviceRevokeIn struct {
	Device int64 `desc:"Device id"`
}

// deviceRevoke signs a device out for good: its sessions and pushes end with the revoke, its open streams after it;
// its passkey signs its browser in again only as a new device.
func (s *Service) deviceRevoke(ctx context.Context, act registry.Actor, in deviceRevokeIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return revoke(ctx, q, act.Workspace, in.Device, s.Now())
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no device %d that may still sign in", in.Device)
	}
	s.Kick(in.Device)
	s.Journal(ctx, act, 0, registry.Deck("device_revoke"), registry.External, "done", fmt.Sprintf(`{"device": %d}`, in.Device))
	return map[string]any{"revoked": in.Device}, nil
}

// revoke revokes a device and ends its sessions; the folders of a node leave the projects that had them.
func revoke(ctx context.Context, q *db.Queries, space, device int64, now time.Time) (int64, error) {
	n, err := q.RevokeDevice(ctx, db.RevokeDeviceParams{RevokedAt: store.At(now), ID: device, WorkspaceID: space})
	if err != nil || n == 0 {
		return n, err
	}
	if err := q.RemoveNodeFolders(ctx, device); err != nil {
		return n, err
	}
	return n, q.DeleteDeviceSessions(ctx, device)
}

// passkeyView is a passkey of the owner: one synced to many browsers signs each in as a device of its own.
type passkeyView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Site      string `json:"site"`
	Devices   int64  `json:"devices"`
	CreatedAt string `json:"created_at"`
	SeenAt    string `json:"seen_at,omitempty"`
	Revoked   bool   `json:"revoked"`
}

func (s *Service) passkeysList(ctx context.Context, act registry.Actor, _ struct{}) ([]passkeyView, error) {
	ks, err := s.St.Q.ListPasskeys(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]passkeyView, 0, len(ks))
	for _, row := range ks {
		k := row.Passkey
		out = append(out, passkeyView{ID: k.ID, Name: k.Name, Site: k.RpID, Devices: row.Devices, CreatedAt: k.CreatedAt, SeenAt: k.SeenAt.String,
			Revoked: k.RevokedAt.Valid})
	}
	return out, nil
}

type passkeyRenameIn struct {
	Passkey int64  `desc:"Passkey id"`
	Name    string `desc:"New name of the passkey"`
}

func (s *Service) passkeyRename(ctx context.Context, act registry.Actor, in passkeyRenameIn) (any, error) {
	name := untrusted.Cut(strings.TrimSpace(in.Name), 80)
	if name == "" {
		return nil, registry.Invalid("a passkey needs a name")
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RenamePasskey(ctx, db.RenamePasskeyParams{Name: name, ID: in.Passkey, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no passkey %d", in.Passkey)
	}
	return map[string]any{"passkey": in.Passkey, "name": name}, nil
}

type passkeyRevokeIn struct {
	Passkey int64 `desc:"Passkey id"`
}

// passkeyRevoke refuses a passkey from now on and revokes every device it signed in last.
func (s *Service) passkeyRevoke(ctx context.Context, act registry.Actor, in passkeyRevokeIn) (any, error) {
	var devices []int64
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		n, err := q.RevokePasskey(ctx, db.RevokePasskeyParams{RevokedAt: store.At(s.Now()), ID: in.Passkey, WorkspaceID: act.Workspace})
		if err != nil || n == 0 {
			return n, err
		}
		if devices, err = q.PasskeyDevices(ctx, db.PasskeyDevicesParams{PasskeyID: sql.NullInt64{Int64: in.Passkey, Valid: true}, WorkspaceID: act.Workspace}); err != nil {
			return n, err
		}
		for _, d := range devices {
			if _, err := revoke(ctx, q, act.Workspace, d, s.Now()); err != nil {
				return n, err
			}
		}
		return n, nil
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no passkey %d that may still sign in", in.Passkey)
	}
	for _, d := range devices {
		s.Kick(d)
	}
	s.Journal(ctx, act, 0, registry.Deck("passkey_revoke"), registry.External, "done", fmt.Sprintf(`{"passkey": %d}`, in.Passkey))
	return map[string]any{"revoked": in.Passkey, "devices": devices}, nil
}

type pairLinkIn struct {
	Kind string `enum:"browser|node" opt:"" desc:"What pairs: a browser with its passkey or a node; a browser when left out"`
	Name string `opt:"" desc:"Name of the device"`
}

// pairLinkView is a link that pairs one device in ten minutes, at every address of deck where a browser makes a
// passkey, the code in it and the QR code of the first https link for a phone's camera.
type pairLinkView struct {
	QR    string   `json:"qr,omitempty" media:"image/png"`
	Links []string `json:"links"`
	// Join are the commands that pair a node and start it as a service, run on the node's machine.
	Join  []string `json:"join,omitempty"`
	Code  string   `json:"code"`
	Until string   `json:"until"`
}

func (s *Service) pairLink(ctx context.Context, act registry.Actor, in pairLinkIn) (any, error) {
	if in.Kind == "" {
		in.Kind = auth.Browser
	}
	if in.Kind == auth.Browser && len(auth.Links(s.Origins(), "")) == 0 {
		return nil, registry.Refuse("a browser pairs with deck only over https or at deck.localhost on its machine, and deck has neither here; see Health for how to give deck an https address")
	}
	code, until, err := s.Code(act.Workspace, in.Kind, untrusted.Cut(strings.TrimSpace(in.Name), 80))
	if err != nil {
		return nil, err
	}
	v := pairLinkView{Links: auth.Links(s.Origins(), code), Code: code, Until: store.Time(until)}
	if in.Kind != auth.Browser {
		v.Links, v.Join = []string{}, auth.Joins(s.Origins(), code)
	} else if q, err := qr.Encode(v.Links[0], qr.M); err == nil && strings.HasPrefix(v.Links[0], "https://") {
		v.QR = base64.StdEncoding.EncodeToString(q.PNG())
	}
	return v, nil
}

// errNoDevice refuses a call that needs the device it came from when it came from none.
var errNoDevice = errors.New("deck: this call comes from no device of the owner")

func (s *Service) deviceCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("devices_list", "Devices",
			"Lists the owner's devices: each browser with the passkey it signed in with and each node, the one the call came from marked; revoked ones too.",
			registry.OwnerOnly, s.devicesList).On("devices"),
		registry.Def("device_rename", "Rename device", "Renames a device of the owner.", registry.OwnerOnly, s.deviceRename),
		registry.Def("device_revoke", "Revoke device",
			"Signs a device of the owner out for good: its sessions, pushes and open pages end; its passkey signs that browser in again only as a new device.",
			registry.OwnerOnly, s.deviceRevoke).As(registry.External),
		registry.Read("passkeys_list", "Passkeys",
			"Lists the owner's passkeys: one synced to many browsers signs each in as a device of its own; revoked ones too.",
			registry.OwnerOnly, s.passkeysList).On("devices"),
		registry.Def("passkey_rename", "Rename passkey", "Renames a passkey of the owner.", registry.OwnerOnly, s.passkeyRename),
		registry.Def("passkey_revoke", "Revoke passkey",
			"Refuses a passkey of the owner from now on and signs out every device it signed in.",
			registry.OwnerOnly, s.passkeyRevoke).As(registry.External),
		registry.Answers[pairLinkView](registry.Def("pair_link", "Pair a device",
			"Makes a link that pairs one more device of the owner within ten minutes, once: open it on the device, or point its camera at the QR code.",
			registry.OwnerOnly, s.pairLink).As(registry.External)),
	}
}

// Welcome is the link that pairs the first device while no device of the owner may sign in; none after.
func (s *Service) Welcome(ctx context.Context, signed bool) ([]string, error) {
	if signed {
		return nil, nil
	}
	spaces, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil || len(spaces) == 0 {
		return nil, err
	}
	code, _, err := s.Code(spaces[0], auth.Browser, "")
	if err != nil {
		return nil, err
	}
	return auth.Links(s.Origins(), code), nil
}

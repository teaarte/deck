package owners

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"rsc.io/qr"

	"deck2/internal/auth"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// deviceView is a device of the owner with the buttons of its row.
type deviceView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"created_at"`
	SeenAt    string `json:"seen_at,omitempty"`
	// This marks the device the call came from.
	This bool `json:"this"`
	// Passkey says the device signs in with a passkey; a device of the pushes before pairing has none.
	Passkey bool `json:"passkey"`
	Push    bool `json:"push"`
	Revoked bool `json:"revoked"`
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
	for _, d := range ds {
		v := deviceView{ID: d.ID, Name: d.Name, Kind: d.Kind, CreatedAt: d.CreatedAt, SeenAt: d.SeenAt.String, This: d.ID == act.Device,
			Passkey: len(d.CredentialID) > 0, Push: d.PushEndpoint.Valid, Revoked: d.RevokedAt.Valid}
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

// deviceRevoke signs a device out for good: its sessions and pushes end with the revoke, its open streams after it.
func (s *Service) deviceRevoke(ctx context.Context, act registry.Actor, in deviceRevokeIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		n, err := q.RevokeDevice(ctx, db.RevokeDeviceParams{RevokedAt: store.At(s.Now()), ID: in.Device, WorkspaceID: act.Workspace})
		if err != nil || n == 0 {
			return n, err
		}
		return n, q.DeleteDeviceSessions(ctx, in.Device)
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

type pairLinkIn struct {
	Kind string `enum:"browser|launcher|node" opt:"" desc:"What pairs: a browser with its passkey, the window of the launcher or a node; a browser when left out"`
	Name string `opt:"" desc:"Name of the device"`
}

// pairLinkView is a link that pairs one device in ten minutes, at every address of deck where a browser makes a
// passkey, the code in it and the QR code of the first https link for a phone's camera.
type pairLinkView struct {
	QR    string   `json:"qr,omitempty" media:"image/png"`
	Links []string `json:"links"`
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
		v.Links = []string{}
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
			"Lists the owner's devices: browsers with their passkeys, the window of the launcher and nodes, the one the call came from marked; revoked ones too.",
			registry.OwnerOnly, s.devicesList).On("devices"),
		registry.Def("device_rename", "Rename device", "Renames a device of the owner.", registry.OwnerOnly, s.deviceRename),
		registry.Def("device_revoke", "Revoke device",
			"Signs a device of the owner out for good: its sessions, pushes and open pages end; it pairs again only by a new link.",
			registry.OwnerOnly, s.deviceRevoke).As(registry.External),
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

package owners

import (
	"context"
	"database/sql"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type pushSubscribeIn struct {
	Endpoint string `desc:"Push endpoint of the device"`
	P256dh   string `desc:"Key of the subscription"`
	Auth     string `desc:"Auth secret of the subscription"`
}

// pushSubscribe gives the device the call came from pushes, by the subscription its browser made.
func (s *Service) pushSubscribe(ctx context.Context, act registry.Actor, in pushSubscribeIn) (any, error) {
	switch {
	case act.Device == 0:
		return nil, registry.Refuse("%s", errNoDevice.Error())
	case !strings.HasPrefix(in.Endpoint, "https://") && !strings.HasPrefix(in.Endpoint, "http://"):
		return nil, registry.Invalid("a push endpoint is an http or https url")
	case in.P256dh == "" || in.Auth == "":
		return nil, registry.Invalid("a push subscription needs its keys p256dh and auth")
	}
	endpoint := sql.NullString{String: in.Endpoint, Valid: true}
	var row db.Device
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		before, err := q.ListPushDevices(ctx, act.Workspace)
		if err != nil {
			return err
		}
		if len(before) == 0 {
			// The first device starts from now, not from whatever came while no device listened.
			if err := q.DeletePushMarks(ctx, act.Workspace); err != nil {
				return err
			}
		}
		if err := q.DropPushEndpoint(ctx, db.DropPushEndpointParams{Endpoint: endpoint, ID: act.Device}); err != nil {
			return err
		}
		row, err = q.SetDevicePush(ctx, db.SetDevicePushParams{PushEndpoint: endpoint, PushP256dh: in.P256dh, PushAuth: in.Auth,
			ID: act.Device, WorkspaceID: act.Workspace})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.PushNew(ctx, act.Workspace)
	return map[string]any{"device": row.ID, "name": row.Name}, nil
}

type pushUnsubscribeIn struct {
	Endpoint string `opt:"" desc:"Push endpoint of the device"`
	Device   int64  `opt:"" desc:"Device id"`
}

func (s *Service) pushUnsubscribe(ctx context.Context, act registry.Actor, in pushUnsubscribeIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.DeletePush(ctx, db.DeletePushParams{WorkspaceID: act.Workspace, Endpoint: sql.NullString{String: in.Endpoint, Valid: in.Endpoint != ""}, ID: in.Device})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no such device")
	}
	return map[string]any{"removed": n}, nil
}

func (s *Service) pushCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("push_subscribe", "Get pushes",
			"Subscribes the device the call comes from to pushes: what waits for the owner and the notes of the feed when no client is open.",
			registry.OwnerOnly, s.pushSubscribe).Stays(),
		registry.Def("push_unsubscribe", "Stop pushes",
			"Stops pushes to a device.",
			registry.OwnerOnly, s.pushUnsubscribe).Stays(),
	}
}

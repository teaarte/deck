package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"deck2/internal/app/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	// vapidSecret keeps the key pair deck signs its pushes with.
	vapidSecret = "push.vapid"
	pushLength  = 100
	// pushPause gathers the changes of a moment into one look for what to push.
	pushPause = 500 * time.Millisecond
	// generic is what a push of a project without network says instead of its text.
	generic = "Something in deck waits for you"
)

// pushMessage is what the service worker of a device shows: a title, a short text and where a tap goes.
type pushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

type vapid struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// vapidKeys are the keys of the workspace, made on first use.
func (a *App) vapidKeys(workspace int64) (vapid, error) {
	a.secretMu.Lock()
	defer a.secretMu.Unlock()
	var k vapid
	if raw, err := a.Secret(workspace, vapidSecret); err == nil && json.Unmarshal([]byte(raw), &k) == nil && k.Public != "" {
		return k, nil
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	k = vapid{Public: public, Private: private}
	raw, _ := json.Marshal(k)
	return k, a.writeSecret(workspace, vapidSecret, string(raw))
}

func (a *App) pushKey(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	k, err := a.vapidKeys(act.Workspace)
	if err != nil {
		return nil, err
	}
	return map[string]any{"key": k.Public}, nil
}

func (a *App) pushSubscribe(ctx context.Context, act registry.Actor, in struct {
	Endpoint, P256dh, Auth, Device string
}) (any, error) {
	switch {
	case !strings.HasPrefix(in.Endpoint, "https://") && !strings.HasPrefix(in.Endpoint, "http://"):
		return nil, registry.Invalid("deck: a push endpoint is an http or https url")
	case in.P256dh == "" || in.Auth == "":
		return nil, registry.Invalid("deck: a push subscription needs its keys p256dh and auth")
	}
	device := strings.TrimSpace(in.Device)
	if device == "" {
		device = "a device"
	}
	var row db.PushSubscription
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		row, err = q.AddPushSubscription(ctx, db.AddPushSubscriptionParams{WorkspaceID: act.Workspace, Device: untrusted.Cut(device, 80),
			Endpoint: in.Endpoint, P256dh: in.P256dh, Auth: in.Auth})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.pushNew(ctx, act.Workspace)
	a.Bus.Publish("settings")
	return map[string]any{"device": row.ID, "name": row.Device}, nil
}

func (a *App) pushUnsubscribe(ctx context.Context, act registry.Actor, in struct {
	Endpoint string
	Device   int64
}) (any, error) {
	var n int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.DeletePushSubscription(ctx, db.DeletePushSubscriptionParams{WorkspaceID: act.Workspace, Endpoint: in.Endpoint, ID: in.Device})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no such device")
	}
	a.Bus.Publish("settings")
	return map[string]any{"removed": n}, nil
}

func (a *App) pushDevices(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	rows, err := a.Store.Q.ListPushSubscriptions(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "name": r.Device, "created_at": r.CreatedAt})
	}
	return out, nil
}

// watchPush looks for what to push after the changes that can bring it, a moment after they settle.
func (a *App) watchPush() {
	var mu = make(chan struct{}, 1)
	a.Bus.Watch(func(topic string) {
		if !slices.Contains([]string{"asks", "processes", "notifications", "chat"}, topic) {
			return
		}
		select {
		case mu <- struct{}{}:
		default:
			return
		}
		time.AfterFunc(pushPause, func() {
			<-mu
			if a.ctx.Err() != nil {
				return
			}
			spaces, _ := a.Store.Q.ListWorkspaces(a.ctx)
			for _, ws := range spaces {
				a.pushNew(a.ctx, ws)
			}
		})
	})
}

// pushNew pushes to the devices of a workspace what came since the last look: asks that wait for the owner,
// failed processes, lines of notify, and the master's replies while no client is open.
func (a *App) pushNew(ctx context.Context, workspace int64) {
	a.pushMu.Lock()
	defer a.pushMu.Unlock()
	subs, err := a.Store.Q.ListPushSubscriptions(ctx, workspace)
	if err != nil || len(subs) == 0 {
		return
	}
	mark := func(key string) (int64, bool) {
		v, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: "push:" + key})
		n, _ := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	set := func(key, v string) {
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: workspace, Key: "push:" + key, Value: v})
		})
	}
	var out []pushMessage
	if last, ok := mark("ask"); !ok {
		max, _ := a.Store.Q.MaxAskID(ctx, workspace)
		set("ask", fmt.Sprint(max))
	} else if asks, err := a.Store.Q.ListAsksAfter(ctx, db.ListAsksAfterParams{WorkspaceID: workspace, ID: last}); err == nil && len(asks) > 0 {
		for _, q := range asks {
			out = append(out, a.pushText(ctx, q.ProjectID, "Waiting for you", strings.SplitN(q.Text, "\n", 2)[0], "/#/deck/cockpit"))
		}
		set("ask", fmt.Sprint(asks[len(asks)-1].ID))
	}
	since, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: "push:failed"})
	if err != nil {
		set("failed", store.Time(a.Cfg.Now()))
	} else if failed, err := a.Store.Q.ListFailedAfter(ctx, db.ListFailedAfterParams{WorkspaceID: workspace,
		FinishedAt: sql.NullString{String: since, Valid: true}}); err == nil && len(failed) > 0 {
		for _, p := range failed {
			proj, _ := a.Store.Q.GetProject(ctx, p.ProjectID)
			out = append(out, a.pushText(ctx, p.ProjectID, proj.Name+"."+p.Command+" failed", p.Error, fmt.Sprintf("/#/deck/process/%d", p.ID)))
		}
		set("failed", failed[len(failed)-1].FinishedAt.String)
	}
	if last, ok := mark("notify"); !ok {
		max, _ := a.Store.Q.MaxNotificationID(ctx, workspace)
		set("notify", fmt.Sprint(max))
	} else if ns, err := a.Store.Q.ListNotificationsAfter(ctx, db.ListNotificationsAfterParams{WorkspaceID: workspace, ID: last}); err == nil && len(ns) > 0 {
		for _, n := range ns {
			proj, _ := a.Store.Q.GetProject(ctx, n.ProjectID)
			url := "/#/deck/cockpit"
			if n.Screen != "" {
				url = "/#/p/" + proj.Name + "/" + n.Screen
				if n.RecordID != 0 {
					url += fmt.Sprintf("/%d", n.RecordID)
				}
			}
			out = append(out, a.pushText(ctx, n.ProjectID, proj.Name, n.Text, url))
		}
		set("notify", fmt.Sprint(ns[len(ns)-1].ID))
	}
	if last, ok := mark("chat"); !ok {
		max, _ := a.Store.Q.MaxMasterMessageID(ctx, workspace)
		set("chat", fmt.Sprint(max))
	} else if ms, err := a.Store.Q.ListMasterMessagesAfter(ctx, db.ListMasterMessagesAfterParams{WorkspaceID: workspace, ID: last}); err == nil && len(ms) > 0 {
		if a.Bus.Clients() == 0 {
			for _, m := range ms {
				out = append(out, pushMessage{Title: "Master", Body: untrusted.Cut(oneLine(m.Text), pushLength), URL: "/#/deck/cockpit"})
			}
		}
		set("chat", fmt.Sprint(ms[len(ms)-1].ID))
	}
	if len(out) == 0 {
		return
	}
	k, err := a.vapidKeys(workspace)
	if err != nil {
		slog.Error("push keys", "err", err)
		return
	}
	for _, m := range out {
		for _, s := range subs {
			a.sendPush(ctx, workspace, k, s, m)
		}
	}
}

// pushText is a push about something of a project: its text cut short, or only a generic line for a project
// whose data stays off the network.
func (a *App) pushText(ctx context.Context, projectID int64, title, text, url string) pushMessage {
	if p, err := a.Store.Q.GetProject(ctx, projectID); err == nil {
		if m, err := docs.Manifest(ctx, a.Store.Q, p); err == nil && m.NoNetwork {
			return pushMessage{Title: "deck", Body: generic, URL: url}
		}
	}
	return pushMessage{Title: untrusted.Cut(title, 60), Body: untrusted.Cut(oneLine(text), pushLength), URL: url}
}

func (a *App) sendPush(ctx context.Context, workspace int64, k vapid, s db.PushSubscription, m pushMessage) {
	payload, _ := json.Marshal(m)
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: s.Endpoint,
		Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}, &webpush.Options{Subscriber: "mailto:deck@localhost",
		VAPIDPublicKey: k.Public, VAPIDPrivateKey: k.Private, TTL: 3600, Urgency: webpush.UrgencyHigh})
	if err != nil {
		slog.Error("push", "device", s.Device, "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeletePushSubscription(ctx, db.DeletePushSubscriptionParams{WorkspaceID: workspace, Endpoint: s.Endpoint})
			return err
		})
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

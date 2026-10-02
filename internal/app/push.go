package app

import (
	"context"
	"database/sql"
	"encoding/base64"
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

	"deck2/internal/docs"
	"deck2/internal/health"
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
	raw, err := a.Secret(workspace, vapidSecret)
	if err == nil && json.Unmarshal([]byte(raw), &k) == nil && validVAPID(k) {
		return k, nil
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	k = vapid{Public: public, Private: private}
	b, _ := json.Marshal(k)
	if err := a.writeSecret(workspace, vapidSecret, string(b)); err != nil || raw == "" {
		return k, err
	}
	// Subscriptions made with the old key would fail now; their devices subscribe again.
	return k, a.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.DeleteWorkspacePushSubscriptions(ctx, workspace)
	})
}

func validVAPID(k vapid) bool {
	pub, err1 := base64.RawURLEncoding.DecodeString(k.Public)
	priv, err2 := base64.RawURLEncoding.DecodeString(k.Private)
	return err1 == nil && err2 == nil && len(pub) == 65 && len(priv) == 32
}

func (a *App) pushKey(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	k, err := a.vapidKeys(act.Workspace)
	if err != nil {
		return nil, err
	}
	return map[string]any{"key": k.Public}, nil
}

type pushSubscribeIn struct {
	Endpoint string `desc:"Push endpoint of the device"`
	P256dh   string `desc:"Key of the subscription"`
	Auth     string `desc:"Auth secret of the subscription"`
	Device   string `opt:"" desc:"Name of the device"`
}

func (a *App) pushSubscribe(ctx context.Context, act registry.Actor, in pushSubscribeIn) (any, error) {
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
		before, err := q.ListPushSubscriptions(ctx, act.Workspace)
		if err != nil {
			return err
		}
		if len(before) == 0 {
			// The first device starts from now, not from whatever came while no device listened.
			if err := q.DeleteMarksWithPrefix(ctx, db.DeleteMarksWithPrefixParams{WorkspaceID: act.Workspace, Prefix: "push:"}); err != nil {
				return err
			}
		}
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

type pushUnsubscribeIn struct {
	Endpoint string `opt:"" desc:"Push endpoint of the device"`
	Device   int64  `opt:"" desc:"Device id"`
}

func (a *App) pushUnsubscribe(ctx context.Context, act registry.Actor, in pushUnsubscribeIn) (any, error) {
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

// pushItem is one push and the cursor of its source once it is delivered; an empty push only moves the cursor.
type pushItem struct {
	msg    pushMessage
	cursor string
}

// pushBatch is how many pushes of one source one look sends; the rest wait for the next look.
const pushBatch = 10

// pushSource is one kind of thing deck pushes: where its cursor starts, and what came after a cursor with the
// cursor past each.
type pushSource struct {
	key   string
	start func(ctx context.Context, workspace int64) string
	after func(ctx context.Context, workspace int64, cursor string) []pushItem
}

// pushSources are what waits for the owner, failures, notify lines and the master's replies while no client is open.
func (a *App) pushSources() []pushSource {
	id := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
	return []pushSource{
		{"ask", func(ctx context.Context, ws int64) string { n, _ := a.Store.Q.MaxAskID(ctx, ws); return fmt.Sprint(n) },
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				asks, _ := a.Store.Q.ListAsksAfter(ctx, db.ListAsksAfterParams{WorkspaceID: ws, ID: id(cursor)})
				var items []pushItem
				for _, q := range asks {
					items = append(items, pushItem{a.pushText(ctx, q.ProjectID, "Waiting for you", strings.SplitN(q.Text, "\n", 2)[0], "/#/deck/cockpit"),
						fmt.Sprint(q.ID)})
				}
				return items
			}},
		{"failed", func(context.Context, int64) string { return store.Time(a.Cfg.Now()) + "|0" },
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				at, last, _ := strings.Cut(cursor, "|")
				failed, _ := a.Store.Q.ListFailedAfter(ctx, db.ListFailedAfterParams{WorkspaceID: ws, FinishedAt: sql.NullString{String: at, Valid: true}, ID: id(last)})
				var items []pushItem
				for _, f := range failed {
					p := f.Process
					items = append(items, pushItem{a.pushText(ctx, p.ProjectID, f.ProjectName+"."+p.Command+" failed", p.Error, fmt.Sprintf("/#/deck/process/%d", p.ID)),
						p.FinishedAt.String + "|" + fmt.Sprint(p.ID)})
				}
				return items
			}},
		{"notify", func(ctx context.Context, ws int64) string {
			n, _ := a.Store.Q.MaxNotificationID(ctx, ws)
			return fmt.Sprint(n)
		},
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				ns, _ := a.Store.Q.ListNotificationsAfter(ctx, db.ListNotificationsAfterParams{WorkspaceID: ws, ID: id(cursor)})
				var items []pushItem
				for _, row := range ns {
					n := row.Notification
					url := "/#/deck/cockpit"
					if n.Screen != "" {
						url = "/#/p/" + row.ProjectName + "/" + n.Screen
						if n.RecordID.Valid {
							url += fmt.Sprintf("/%d", n.RecordID.Int64)
						}
					}
					items = append(items, pushItem{a.pushText(ctx, n.ProjectID, row.ProjectName, n.Text, url), fmt.Sprint(n.ID)})
				}
				return items
			}},
		{"chat", func(ctx context.Context, ws int64) string {
			n, _ := a.Store.Q.MaxMasterMessageID(ctx, ws)
			return fmt.Sprint(n)
		},
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				ms, _ := a.Store.Q.ListMasterMessagesAfter(ctx, db.ListMasterMessagesAfterParams{WorkspaceID: ws, ID: id(cursor)})
				private := a.anyPrivate(ctx, ws)
				var items []pushItem
				for _, m := range ms {
					msg := pushMessage{Title: "Master", Body: untrusted.Cut(oneLine(m.Text), pushLength), URL: "/#/deck/chat"}
					if private {
						msg.Body = "The master replied"
					}
					if a.Bus.Clients() > 0 {
						msg = pushMessage{}
					}
					items = append(items, pushItem{msg, fmt.Sprint(m.ID)})
				}
				return items
			}},
	}
}

// pushNew pushes what came since the last delivered push of each source; a cursor moves only past what a device
// took or refused for good, and past a reply of the master that came while a client was open.
func (a *App) pushNew(ctx context.Context, workspace int64) {
	a.pushMu.Lock()
	defer a.pushMu.Unlock()
	subs, err := a.Store.Q.ListPushSubscriptions(ctx, workspace)
	if err != nil || len(subs) == 0 {
		return
	}
	set := func(key, v string) {
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: workspace, Name: "push:" + key, Value: v})
		})
	}
	var k vapid
	for _, src := range a.pushSources() {
		cursor, err := a.Store.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: workspace, Name: "push:" + src.key})
		if err != nil {
			set(src.key, src.start(ctx, workspace))
			continue
		}
		items := src.after(ctx, workspace, cursor)
		if len(items) > pushBatch {
			items = items[:pushBatch]
		}
		for _, it := range items {
			if it.msg == (pushMessage{}) {
				set(src.key, it.cursor)
				continue
			}
			if k.Public == "" {
				if k, err = a.vapidKeys(workspace); err != nil {
					slog.Error("push keys", "err", err)
					return
				}
			}
			if !a.deliver(ctx, workspace, k, subs, it.msg) {
				break
			}
			set(src.key, it.cursor)
		}
	}
}

// anyPrivate reports whether a project of the workspace keeps its data off the network; then the master's
// replies, which may speak of it, push only that the master replied.
func (a *App) anyPrivate(ctx context.Context, workspace int64) bool {
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return true
	}
	for _, p := range ps {
		if m, err := docs.Manifest(ctx, a.Store.Q, p); err != nil || m.NoNetwork {
			return true
		}
	}
	return false
}

// pushText is a push about something of a project: its text cut short, or only a generic line for a project
// whose data stays off the network, the master while any project's does, or a project deck cannot read.
func (a *App) pushText(ctx context.Context, projectID int64, title, text, url string) pushMessage {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil || p.Builtin == 1 && a.anyPrivate(ctx, p.WorkspaceID) {
		return pushMessage{Title: "deck", Body: generic, URL: url}
	}
	if m, err := docs.Manifest(ctx, a.Store.Q, p); err != nil || m.NoNetwork {
		return pushMessage{Title: "deck", Body: generic, URL: url}
	}
	return pushMessage{Title: untrusted.Cut(title, 60), Body: untrusted.Cut(oneLine(text), pushLength), URL: url}
}

var pushClient = &http.Client{Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// deliver sends one push to every device; false when a device failed in a way worth a retry at the next look.
// A device the service no longer knows goes away; one that refuses the push for good is only logged.
func (a *App) deliver(ctx context.Context, workspace int64, k vapid, subs []db.PushSubscription, m pushMessage) bool {
	payload, _ := json.Marshal(m)
	retry := false
	for _, s := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: s.Endpoint,
			Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}, &webpush.Options{HTTPClient: pushClient, Subscriber: "deck@example.com",
			VAPIDPublicKey: k.Public, VAPIDPrivateKey: k.Private, TTL: 3600, Urgency: webpush.UrgencyHigh})
		if err != nil {
			slog.Error("push", "device", s.Device, "err", err)
			a.pushHealth(ctx, s.Device+": "+err.Error())
			retry = true
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch code := resp.StatusCode; {
		case code >= 200 && code < 300:
			a.pushHealth(ctx, "")
		case code == http.StatusGone || code == http.StatusNotFound:
			_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				_, err := q.DeletePushSubscription(ctx, db.DeletePushSubscriptionParams{WorkspaceID: workspace, Endpoint: s.Endpoint})
				return err
			})
		case code == http.StatusTooManyRequests || code >= 500:
			a.pushHealth(ctx, fmt.Sprintf("%s: %s", s.Device, resp.Status))
			retry = true
		default:
			slog.Error("push refused", "device", s.Device, "status", resp.Status)
			a.pushHealth(ctx, fmt.Sprintf("%s: the push service refused it: %s", s.Device, resp.Status))
		}
	}
	return !retry
}

func (a *App) pushHealth(ctx context.Context, failed string) {
	status := "ok"
	if failed != "" {
		status = "error"
	}
	health.Write(ctx, a.Store, a.Bus, health.Of{}, "push", status, failed)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (a *App) pushCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("push_key", "Push key",
			"Returns the public key a device subscribes to the pushes of deck with.",
			registry.OwnerOnly, a.pushKey),
		registry.Def("push_subscribe", "Get pushes",
			"Subscribes a device to pushes: what waits for the owner, failures, notify lines and the master's replies when no client is open.",
			registry.OwnerOnly, a.pushSubscribe),
		registry.Def("push_unsubscribe", "Stop pushes",
			"Stops pushes to a device.",
			registry.OwnerOnly, a.pushUnsubscribe),
		registry.Def("push_devices", "Devices",
			"Lists the devices that get pushes.",
			registry.OwnerOnly, a.pushDevices),
	}
}

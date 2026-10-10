// Package devices reaches the owner outside the open client: the feed lines of notify, pushes to devices, and
// speech turned into text.
package devices

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	pushLength = 100
	// generic is what a push of a project without network says instead of its text.
	generic = "Something in deck waits for you"
)

// pushMessage is what the service worker of a device shows: a title, a short text and where a tap goes.
type pushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	// Badge is how many decisions wait for the owner, for the icon of the app.
	Badge int64 `json:"badge"`
}

// Kinds of lines of the owner's feed: a note someone wrote with notify, a change deck made without the owner.
const (
	FeedNote   = "note"
	FeedChange = "change"
)

type vapid struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// vapidKeys are the keys of the workspace, made on first use.
func (dv *Service) vapidKeys(workspace int64) (vapid, error) {
	dv.keyMu.Lock()
	defer dv.keyMu.Unlock()
	path := dv.Home.PushKey(workspace)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return vapid{}, err
	}
	var k vapid
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return k, err
	}
	if err == nil && json.Unmarshal(raw, &k) == nil && validVAPID(k) {
		return k, nil
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return k, err
	}
	k = vapid{Public: public, Private: private}
	b, _ := json.Marshal(k)
	if err := os.WriteFile(path, b, 0o600); err != nil || len(raw) == 0 {
		return k, err
	}
	// Subscriptions made with the old key would fail now; their devices subscribe again.
	return k, dv.St.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.DeleteWorkspacePushSubscriptions(ctx, workspace)
	})
}

func validVAPID(k vapid) bool {
	pub, err1 := base64.RawURLEncoding.DecodeString(k.Public)
	priv, err2 := base64.RawURLEncoding.DecodeString(k.Private)
	return err1 == nil && err2 == nil && len(pub) == 65 && len(priv) == 32
}

func (dv *Service) pushKey(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	k, err := dv.vapidKeys(act.Workspace)
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

func (dv *Service) pushSubscribe(ctx context.Context, act registry.Actor, in pushSubscribeIn) (any, error) {
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
	err := dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
	dv.PushNew(ctx, act.Workspace)
	return map[string]any{"device": row.ID, "name": row.Device}, nil
}

type pushUnsubscribeIn struct {
	Endpoint string `opt:"" desc:"Push endpoint of the device"`
	Device   int64  `opt:"" desc:"Device id"`
}

func (dv *Service) pushUnsubscribe(ctx context.Context, act registry.Actor, in pushUnsubscribeIn) (any, error) {
	var n int64
	err := dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
	return map[string]any{"removed": n}, nil
}

// pushReset makes a new key for pushes on the next use; the devices subscribed with the old one subscribe again.
func (dv *Service) pushReset(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	dv.keyMu.Lock()
	err := os.Remove(dv.Home.PushKey(act.Workspace))
	dv.keyMu.Unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	err = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.DeleteWorkspacePushSubscriptions(ctx, act.Workspace)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (dv *Service) pushDevices(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	rows, err := dv.St.Q.ListPushSubscriptions(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "name": r.Device, "created_at": r.CreatedAt})
	}
	return out, nil
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
func (dv *Service) pushSources() []pushSource {
	id := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
	return []pushSource{
		{"ask", func(ctx context.Context, ws int64) string { n, _ := dv.St.Q.MaxAskID(ctx, ws); return fmt.Sprint(n) },
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				asks, _ := dv.St.Q.ListAsksAfter(ctx, db.ListAsksAfterParams{WorkspaceID: ws, ID: id(cursor)})
				var items []pushItem
				for _, q := range asks {
					items = append(items, pushItem{dv.pushText(ctx, q.ProjectID.Int64, "Waiting for you", strings.SplitN(q.Text, "\n", 2)[0], "/#/deck/cockpit"),
						fmt.Sprint(q.ID)})
				}
				return items
			}},
		{"failed", func(context.Context, int64) string { return store.Time(dv.Now()) + "|0" },
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				at, last, _ := strings.Cut(cursor, "|")
				failed, _ := dv.St.Q.ListFailedAfter(ctx, db.ListFailedAfterParams{WorkspaceID: ws, FinishedAt: sql.NullString{String: at, Valid: true}, ID: id(last)})
				var items []pushItem
				for _, f := range failed {
					p := f.Process
					items = append(items, pushItem{dv.pushText(ctx, p.ProjectID, f.ProjectName+"."+p.Command+" failed", p.Error, fmt.Sprintf("/#/deck/process/%d", p.ID)),
						p.FinishedAt.String + "|" + fmt.Sprint(p.ID)})
				}
				return items
			}},
		{"notify", func(ctx context.Context, ws int64) string {
			n, _ := dv.St.Q.MaxNoteID(ctx, ws)
			return fmt.Sprint(n)
		},
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				ns, _ := dv.St.Q.ListNotesAfter(ctx, db.ListNotesAfterParams{WorkspaceID: ws, ID: id(cursor)})
				var items []pushItem
				for _, row := range ns {
					n := row.Feed
					url := "/#/deck/cockpit"
					if name, ok := strings.CutPrefix(n.Screen, DeckPrefix); ok {
						url = "/#/deck/" + name
					} else if n.Screen != "" {
						url = "/#/p/" + row.ProjectName + "/" + n.Screen
					}
					if n.Screen != "" && n.RecordID.Valid {
						url += fmt.Sprintf("/%d", n.RecordID.Int64)
					}
					items = append(items, pushItem{dv.pushText(ctx, n.ProjectID, row.ProjectName, n.Text, url), fmt.Sprint(n.ID)})
				}
				return items
			}},
		{"chat", func(ctx context.Context, ws int64) string {
			n, _ := dv.St.Q.MaxMasterMessageID(ctx, ws)
			return fmt.Sprint(n)
		},
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				ms, _ := dv.St.Q.ListMasterMessagesAfter(ctx, db.ListMasterMessagesAfterParams{WorkspaceID: ws, ID: id(cursor)})
				private := dv.anyPrivate(ctx, ws)
				var items []pushItem
				for _, m := range ms {
					msg := pushMessage{Title: "Master", Body: untrusted.Cut(oneLine(m.Text), pushLength), URL: "/#/deck/chat"}
					if private {
						msg.Body = "The master replied"
					}
					items = append(items, pushItem{msg, fmt.Sprint(m.ID)})
				}
				return items
			}},
	}
}

// PushNew pushes to each device what came since the last push it took of each source; a cursor moves only past
// what the device took or refused for good.
func (dv *Service) PushNew(ctx context.Context, workspace int64) {
	dv.pushMu.Lock()
	defer dv.pushMu.Unlock()
	subs, err := dv.St.Q.ListPushSubscriptions(ctx, workspace)
	if err != nil {
		return
	}
	_ = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteStalePushMarks(ctx, workspace) })
	set := func(name, v string) {
		_ = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: workspace, Name: name, Value: v})
		})
	}
	var k vapid
	var failed []string
	tried, lasting := false, false
	badge := int64(0)
	if pulses, err := dv.St.Q.ProjectPulses(ctx, workspace); err == nil {
		for _, p := range pulses {
			badge += p.Asks
		}
	}
	for _, s := range subs {
	sources:
		for _, src := range dv.pushSources() {
			name := fmt.Sprintf("push:%d:%s", s.ID, src.key)
			cursor, err := dv.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: workspace, Name: name})
			if err != nil {
				set(name, src.start(ctx, workspace))
				continue
			}
			items := src.after(ctx, workspace, cursor)
			if len(items) > pushBatch {
				items = items[:pushBatch]
			}
			for _, it := range items {
				if k.Public == "" {
					if k, err = dv.vapidKeys(workspace); err != nil {
						slog.Error("push keys", "err", err)
						return
					}
				}
				tried = true
				it.msg.Badge = badge
				res, why := dv.deliver(ctx, workspace, k, s, it.msg)
				if why != "" {
					failed = append(failed, s.Device+": "+why)
					lasting = lasting || res != pushRetry
				}
				if res != pushDone {
					break sources
				}
				set(name, it.cursor)
			}
		}
	}
	if tried {
		status, text := "ok", strings.Join(failed, "; ")
		if len(failed) > 0 {
			status = "error"
		}
		prev, err := dv.St.Q.GetWorkspaceHealth(ctx, db.GetWorkspaceHealthParams{WorkspaceID: store.ID(workspace), Subject: "push"})
		health.Write(ctx, dv.St, health.Of{Workspace: workspace}, "push", status, text)
		if !lasting || err == nil && prev.Status == "error" {
			return
		}
		err = dv.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			mp, err := access.Master(ctx, q, workspace)
			if err != nil {
				return err
			}
			return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(dv.Now()), ProjectID: mp.ID, Text: "Pushes stopped: " + untrusted.Cut(text, 300)})
		})
		if err != nil {
			slog.Error("note of stopped pushes", "err", err)
		}
	}
}

// anyPrivate reports whether a project of the workspace keeps its data off the network; then the master's
// replies, which may speak of it, push only that the master replied.
func (dv *Service) anyPrivate(ctx context.Context, workspace int64) bool {
	ps, err := dv.St.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return true
	}
	for _, p := range ps {
		if m, err := docs.For(ctx, dv.St.Q, registry.Actor{}, p.ID); err != nil || m.NoNetwork {
			return true
		}
	}
	return false
}

// pushText is a push about something of a project: its text cut short, or only a generic line for a project
// whose data stays off the network, the master while any project's does, or a project deck cannot read.
func (dv *Service) pushText(ctx context.Context, projectID int64, title, text, url string) pushMessage {
	p, err := dv.St.Q.GetProject(ctx, projectID)
	if err != nil || p.Builtin == 1 && dv.anyPrivate(ctx, p.WorkspaceID) {
		return pushMessage{Title: "deck", Body: generic, URL: url}
	}
	if m, err := docs.For(ctx, dv.St.Q, registry.Actor{}, p.ID); err != nil || m.NoNetwork {
		return pushMessage{Title: "deck", Body: generic, URL: url}
	}
	return pushMessage{Title: untrusted.Cut(title, 60), Body: untrusted.Cut(oneLine(text), pushLength), URL: url}
}

var pushClient = &http.Client{Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// What a push to a device came to: taken or refused for good, to be sent again, or the device is gone.
const (
	pushDone = iota
	pushRetry
	pushGone
)

// deliver sends one push to a device and says why it failed, if it did; a device the service no longer knows
// goes away, one that refuses the push for good is only logged.
func (dv *Service) deliver(ctx context.Context, workspace int64, k vapid, s db.PushSubscription, m pushMessage) (int, string) {
	payload, _ := json.Marshal(m)
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: s.Endpoint,
		Keys: webpush.Keys{P256dh: s.P256dh, Auth: s.Auth}}, &webpush.Options{HTTPClient: pushClient, Subscriber: "deck@example.com",
		VAPIDPublicKey: k.Public, VAPIDPrivateKey: k.Private, TTL: 3600, Urgency: webpush.UrgencyHigh})
	if err != nil {
		slog.Error("push", "device", s.Device, "err", err)
		return pushRetry, err.Error()
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	status := resp.Status
	if why := oneLine(string(body)); why != "" {
		status += " (" + untrusted.Cut(why, 200) + ")"
	}
	gone := func() {
		_ = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeletePushSubscription(ctx, db.DeletePushSubscriptionParams{WorkspaceID: workspace, Endpoint: s.Endpoint})
			return err
		})
	}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return pushDone, ""
	case code == http.StatusGone || code == http.StatusNotFound:
		gone()
		return pushGone, ""
	case (code == http.StatusBadRequest || code == http.StatusForbidden) && otherKey(string(body)):
		// The device subscribed with another key of deck: the subscription goes, and the device subscribes again.
		slog.Error("push refused", "device", s.Device, "status", status)
		gone()
		return pushGone, "the push service refused the subscription: " + status + "; deck forgot it, turn pushes on again on this device in Settings → Interface"
	case code == http.StatusTooManyRequests || code >= 500:
		return pushRetry, status
	default:
		slog.Error("push refused", "device", s.Device, "status", status)
		return pushDone, "the push service refused it: " + status + "; turn pushes off and on again on this device in Settings → Interface"
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// otherKey says a push service refused a subscription made with another key: Apple's VapidPkHashMismatch, or
// FCM's credentials of a different sender; a fault of deck's own signature is not.
func otherKey(body string) bool {
	return strings.Contains(body, "VapidPkHashMismatch") || strings.Contains(body, "different sender")
}

// PushCommands are the commands of pushes.
func (dv *Service) PushCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("push_key", "Push key",
			"Returns the public key a device subscribes to the pushes of deck with.",
			registry.OwnerOnly, dv.pushKey),
		registry.Def("push_subscribe", "Get pushes",
			"Subscribes a device to pushes: what waits for the owner, failures, notify lines and the master's replies when no client is open.",
			registry.OwnerOnly, dv.pushSubscribe),
		registry.Def("push_unsubscribe", "Stop pushes",
			"Stops pushes to a device.",
			registry.OwnerOnly, dv.pushUnsubscribe),
		registry.Def("push_reset", "Reset the push key",
			"Makes a new key for pushes; every device subscribed with the old one has to subscribe again.",
			registry.OwnerOnly, dv.pushReset),
		registry.Def("push_devices", "Devices",
			"Lists the devices that get pushes.",
			registry.OwnerOnly, dv.pushDevices),
	}
}

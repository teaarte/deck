// Package devices reaches the owner outside the open client: the feed lines of notify, pushes to devices, and
// speech turned into text.
package feed

import (
	"cmp"
	"context"
	"deck2/internal/events"
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
	"sync"

	"github.com/SherClockHolmes/webpush-go"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/egress"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/internal/words"
)

const (
	pushLength = 100
	// generic is what a push of a project deck cannot read says instead of its text.
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

type vapid struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// vapidKeys are the keys of the workspace, made on first use.
func (s *Service) vapidKeys(workspace int64) (vapid, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	path := s.Home.PushKey(workspace)
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
	if err := home.WriteFile(path, b); err != nil || len(raw) == 0 {
		return k, err
	}
	// Subscriptions made with the old key would fail now; their devices subscribe again.
	return k, s.St.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.DeleteWorkspacePush(ctx, workspace)
	})
}

func validVAPID(k vapid) bool {
	pub, err1 := base64.RawURLEncoding.DecodeString(k.Public)
	priv, err2 := base64.RawURLEncoding.DecodeString(k.Private)
	return err1 == nil && err2 == nil && len(pub) == 65 && len(priv) == 32
}

// pushKeyView is the public key a device subscribes with.
type pushKeyView struct {
	Key string `json:"key"`
}

func (s *Service) pushKey(ctx context.Context, act registry.Actor, _ struct{}) (pushKeyView, error) {
	k, err := s.vapidKeys(act.Workspace)
	return pushKeyView{Key: k.Public}, err
}

// pushReset makes a new key for pushes on the next use; the devices subscribed with the old one subscribe again.
func (s *Service) pushReset(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	s.keyMu.Lock()
	err := os.Remove(s.Home.PushKey(act.Workspace))
	s.keyMu.Unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.DeleteWorkspacePush(ctx, act.Workspace)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
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

// pushSources are what waits for the owner and the notes of the feed, pushed while no client is open, in the words
// say, only the module each is of when hidden.
func (s *Service) pushSources(hidden bool) []pushSource {
	sayOf := func(ctx context.Context, ws, process int64) map[string]string { return words.In(s.Lang(ctx, ws, process)) }
	id := func(str string) int64 { n, _ := strconv.ParseInt(str, 10, 64); return n }
	return []pushSource{
		{"ask", func(ctx context.Context, ws int64) string { n, _ := s.St.Q.MaxAskID(ctx, ws); return fmt.Sprint(n) },
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				asks, _ := s.St.Q.ListAsksAfter(ctx, db.ListAsksAfterParams{WorkspaceID: ws, ID: id(cursor)})
				var items []pushItem
				for _, q := range asks {
					say := sayOf(ctx, ws, q.ProcessID.Int64)
					items = append(items, pushItem{s.pushText(ctx, say, hidden, q.ProjectID.Int64, tr(say, "Waiting for you"), strings.SplitN(q.Text, "\n", 2)[0], "/"+ask.Link(q.ID)),
						fmt.Sprint(q.ID)})
				}
				return items
			}},
		{"notify", func(ctx context.Context, ws int64) string {
			n, _ := s.St.Q.MaxNoteID(ctx, ws)
			return fmt.Sprint(n)
		},
			func(ctx context.Context, ws int64, cursor string) []pushItem {
				ns, _ := s.St.Q.ListNotesAfter(ctx, db.ListNotesAfterParams{WorkspaceID: ws, ID: id(cursor)})
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
					items = append(items, pushItem{s.pushText(ctx, sayOf(ctx, ws, n.ProcessID.Int64), hidden, n.ProjectID, row.ProjectName, n.Text, url), fmt.Sprint(n.ID)})
				}
				return items
			}},
	}
}

// PushNew pushes to each device what came since the last push it took of each source; a cursor moves only past
// what the device took or refused for good.
func (s *Service) PushNew(ctx context.Context, workspace int64) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	subs, err := s.St.Q.ListPushDevices(ctx, workspace)
	if err != nil {
		return
	}
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteStalePushMarks(ctx, workspace) })
	set := func(name, v string) {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: workspace, Name: name, Value: v})
		})
	}
	var k vapid
	var failed []string
	tried, lasting := false, false
	// The badge is counted once the first push is read, so it counts every ask that push names.
	badge := sync.OnceValue(func() int64 {
		n := int64(0)
		if waiting, err := s.Waiting(ctx, registry.Actor{Kind: registry.Owner, Workspace: workspace}); err == nil {
			for _, w := range waiting {
				n += w
			}
		}
		return n
	})
	hidden := s.Hidden(ctx, workspace)
	for _, sub := range subs {
	sources:
		for _, src := range s.pushSources(hidden) {
			name := fmt.Sprintf("feed:push:%d:%s", sub.ID, src.key)
			cursor, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: workspace, Name: name})
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
					if k, err = s.vapidKeys(workspace); err != nil {
						slog.Error("push keys", "err", err)
						return
					}
				}
				tried = true
				it.msg.Badge = badge()
				res, why := s.deliver(ctx, workspace, k, sub, it.msg)
				if why != "" {
					failed = append(failed, sub.Name+": "+why)
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
		stopped := false
		err := s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			prev, err := health.Get(ctx, q, health.Of{Workspace: workspace}, health.Push)
			stopped = lasting && (err != nil || prev.Status != health.Error)
			_, err = health.Set(ctx, q, s.Now(), health.Of{Workspace: workspace}, health.Push, status, text)
			return err
		})
		if err != nil {
			slog.Error("health of pushes", "err", err)
		}
		if !stopped {
			return
		}
		err = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			mp, err := access.Master(ctx, q, workspace)
			if err != nil {
				return err
			}
			return store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(s.Now()), ProjectID: mp.ID, Text: "Pushes stopped: " + untrusted.Cut(text, 300), Cause: health.Push})
		})
		if err != nil {
			slog.Error("note of stopped pushes", "err", err)
		}
	}
}

// pushText is a push about something of a project: its text cut short, only the module when hidden, or a generic
// line for a project deck cannot read.
func (s *Service) pushText(ctx context.Context, say map[string]string, hidden bool, projectID int64, title, text, url string) pushMessage {
	p, err := s.St.Q.GetProject(ctx, projectID)
	switch {
	case err != nil:
		return pushMessage{Title: "deck", Body: tr(say, generic), URL: url}
	case hidden:
		return pushMessage{Title: "deck", Body: fmt.Sprintf(tr(say, "New in %s"), p.Name), URL: url}
	}
	return pushMessage{Title: untrusted.Cut(title, 60), Body: untrusted.Cut(oneLine(text), pushLength), URL: url}
}

// tr is a phrase of deck in the words say, as it is without one.
func tr(say map[string]string, phrase string) string { return cmp.Or(say[phrase], phrase) }

// What a push to a device came to: taken or refused for good, to be sent again, or the device is gone.
const (
	pushDone = iota
	pushRetry
	pushGone
)

// deliver sends one push to a device and says why it failed, if it did; a device the service no longer knows
// goes away, one that refuses the push for good is only logged.
func (s *Service) deliver(ctx context.Context, workspace int64, k vapid, sub db.Device, m pushMessage) (int, string) {
	payload, _ := json.Marshal(m)
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: sub.PushEndpoint.String,
		Keys: webpush.Keys{P256dh: sub.PushP256dh, Auth: sub.PushAuth}}, &webpush.Options{HTTPClient: egress.Push, Subscriber: "deck@example.com",
		VAPIDPublicKey: k.Public, VAPIDPrivateKey: k.Private, TTL: 3600, Urgency: webpush.UrgencyHigh})
	if err != nil {
		slog.Error("push", "device", sub.Name, "err", err)
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
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeletePush(ctx, db.DeletePushParams{WorkspaceID: workspace, Endpoint: sub.PushEndpoint})
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
		slog.Error("push refused", "device", sub.Name, "status", status)
		gone()
		return pushGone, "the push service refused the subscription: " + status + "; deck forgot it, turn pushes on again on this device in Settings → Advanced"
	case code == http.StatusTooManyRequests || code >= 500:
		return pushRetry, status
	default:
		slog.Error("push refused", "device", sub.Name, "status", status)
		return pushDone, "the push service refused it: " + status + "; turn pushes off and on again on this device in Settings → Advanced"
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// otherKey says a push service refused a subscription made with another key: Apple's VapidPkHashMismatch, or
// FCM's credentials of a different sender; a fault of deck's own signature is not.
func otherKey(body string) bool {
	return strings.Contains(body, "VapidPkHashMismatch") || strings.Contains(body, "different sender")
}

func (s *Service) pushCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("push_key_get", "Push key",
			"Returns the public key a device subscribes to the pushes of deck with.",
			registry.OwnerOnly, s.pushKey).On(events.Settings),
		registry.Def("push_key_reset", "Reset the push key",
			"Makes a new key for pushes; every device subscribed with the old one has to subscribe again.",
			registry.OwnerOnly, s.pushReset),
	}
}

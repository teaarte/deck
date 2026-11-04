package owner

import (
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

const iPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Mobile/15E148 Safari/604.1"

type passkey struct {
	ID      int64
	Name    string
	Site    string
	Devices int64
	Revoked bool
}

type browserDevice struct {
	device
	PasskeyID int64 `json:"passkey_id"`
	Push      bool
}

func browsers(t *testing.T, e *harness.Env) []browserDevice {
	t.Helper()
	var ds []browserDevice
	e.Must("deck.devices_list", map[string]any{}, &ds)
	return ds
}

func named(ds []browserDevice, name string) browserDevice {
	i := slices.IndexFunc(ds, func(d browserDevice) bool { return d.Name == name })
	if i < 0 {
		return browserDevice{}
	}
	return ds[i]
}

func subscribe(t *testing.T, c harness.Client, endpoint string) {
	t.Helper()
	c.Must("deck.push_subscribe", map[string]any{"endpoint": endpoint, "p256dh": "k", "auth": "a"}, nil)
}

// TestOnePasskeySignsInEachBrowserAsADeviceOfItsOwn: a synced passkey signs each browser in as its own device with its
// own pushes, revoked alone; revoking the passkey signs both out and refuses it.
func TestOnePasskeySignsInEachBrowserAsADeviceOfItsOwn(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	mac, macCookie := pairSecond(t, e, "Mac — Safari")
	phone := mac.Synced()
	phone.Agent = iPhone
	phoneCookie, err := phone.Login(e.URL, e.Https())
	if err != nil {
		t.Fatal(err)
	}
	ds := browsers(t, e)
	m, p := named(ds, "Mac — Safari"), named(ds, "iPhone — Safari")
	if m.ID == 0 || p.ID == 0 || m.ID == p.ID || m.PasskeyID == 0 || m.PasskeyID != p.PasskeyID || p.Passkey != "Mac — Safari" {
		t.Fatalf("the phone signs in by the Mac's passkey as a device of its own, named by its browser: %+v", ds)
	}
	var ks []passkey
	e.Must("deck.passkeys_list", map[string]any{}, &ks)
	if k := ks[slices.IndexFunc(ks, func(k passkey) bool { return k.ID == m.PasskeyID })]; k.Devices != 2 || !strings.HasPrefix(e.Https(), "https://"+k.Site) {
		t.Fatalf("the passkey signs in two devices at its address: %+v", ks)
	}

	again, err := mac.Login(e.URL, e.Https())
	if err != nil {
		t.Fatal(err)
	}
	if after := browsers(t, e); len(after) != len(ds) {
		t.Fatalf("the Mac signs in again as its own device: %+v", after)
	}
	macCookie = again
	var on []browserDevice
	e.Signed(macCookie).Must("deck.devices_list", map[string]any{}, &on)
	if !named(on, "Mac — Safari").This {
		t.Fatalf("the Mac's new session is on its device: %+v", on)
	}

	subscribe(t, e.Signed(macCookie), "https://push.example/mac")
	subscribe(t, e.Signed(phoneCookie), "https://push.example/phone")
	ds = browsers(t, e)
	if !named(ds, "Mac — Safari").Push || !named(ds, "iPhone — Safari").Push {
		t.Fatalf("each browser keeps its own pushes: %+v", ds)
	}

	e.Must("deck.device_revoke", map[string]any{"device": p.ID}, nil)
	if code, _ := e.Signed(phoneCookie).Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("the revoked phone is signed out: %d", code)
	}
	if code, out := e.Signed(macCookie).Call("deck.menu_get", map[string]any{}); code != 200 {
		t.Fatalf("the Mac stays signed in: %d %s", code, out)
	}
	if ds = browsers(t, e); !named(ds, "Mac — Safari").Push || named(ds, "iPhone — Safari").Push {
		t.Fatalf("the Mac keeps its pushes, the phone has none: %+v", ds)
	}
	phoneCookie, err = phone.Login(e.URL, e.Https())
	if err != nil {
		t.Fatal(err)
	}
	if ds = browsers(t, e); len(ds) != 4 || ds[3].Name != "iPhone — Safari" || ds[3].Revoked || ds[3].ID == p.ID {
		t.Fatalf("the passkey signs the revoked phone in again only as a new device: %+v", ds)
	}

	// A second passkey made in the Mac's browser signs it in as the same device and ends the sessions of the first.
	var link struct{ Code string }
	e.Must("deck.pair_link", map[string]any{}, &link)
	second := mac.Another()
	if _, err := second.Pair(e.URL, e.Https(), link.Code, ""); err != nil {
		t.Fatal(err)
	}
	if after := browsers(t, e); len(after) != len(ds) || named(after, "Mac — Safari").PasskeyID == m.PasskeyID {
		t.Fatalf("the Mac stays one device, now of its second passkey: %+v", after)
	}
	if code, _ := e.Signed(macCookie).Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("the session of the first passkey on the device ends: %d", code)
	}
	if macCookie, err = mac.Login(e.URL, e.Https()); err != nil {
		t.Fatal(err)
	}
	m = named(browsers(t, e), "Mac — Safari")
	if m.PasskeyID != p.PasskeyID {
		t.Fatalf("the first passkey signs the Mac in again: %+v", m)
	}

	macStream, phoneStream := stream(t, e, macCookie), stream(t, e, phoneCookie)
	e.Must("deck.passkey_revoke", map[string]any{"passkey": m.PasskeyID}, nil)
	harness.Receive(t, "the stream of the Mac to end", macStream)
	harness.Receive(t, "the stream of the phone to end", phoneStream)
	for _, c := range []string{macCookie, phoneCookie} {
		if code, _ := e.Signed(c).Call("deck.menu_get", map[string]any{}); code != 401 {
			t.Fatalf("a revoked passkey signs every device of it out: %d", code)
		}
	}
	if _, err := phone.Login(e.URL, e.Https()); err == nil {
		t.Fatal("a revoked passkey signs nothing in")
	}
	e.Must("deck.passkeys_list", map[string]any{}, &ks)
	if !ks[slices.IndexFunc(ks, func(k passkey) bool { return k.ID == m.PasskeyID })].Revoked {
		t.Fatalf("the list says the passkey is revoked: %+v", ks)
	}
	for _, d := range browsers(t, e) {
		if d.PasskeyID == m.PasskeyID && !d.Revoked {
			t.Fatalf("the devices of a revoked passkey are revoked: %+v", d)
		}
	}
	e.Refused("deck.passkey_revoke", map[string]any{"passkey": m.PasskeyID}, 404, "may still sign in")
	e.Must("deck.menu_get", map[string]any{}, nil)
}

// TestTheSessionsOfADeviceBeforePasskeysSettleOnTheirBrowsers: of two sessions an older deck put on one device, the
// first browser to call keeps the device, the other gets its own, neither its pushes; one of pushes only stays one.
func TestTheSessionsOfADeviceBeforePasskeysSettleOnTheirBrowsers(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	mac, macCookie := pairSecond(t, e, "MacBook")
	phoneCookie, err := mac.Synced().Login(e.URL, e.Https())
	if err != nil {
		t.Fatal(err)
	}
	ds := browsers(t, e)
	m, p := named(ds, "MacBook"), ds[len(ds)-1]
	// The deck before kept the phone's session on the device of the passkey it signed in with.
	e.SQL(`UPDATE session SET device_id = ? WHERE device_id = ?`, m.ID, p.ID)
	// Nor had it a device of the phone.
	e.SQL(`DELETE FROM device WHERE id = ?`, p.ID)
	// One of the two browsers took the pushes of the device.
	e.SQL(`UPDATE device SET push_endpoint = 'https://push.example/one', push_p256dh = 'k', push_auth = 'a' WHERE id = ?`, m.ID)
	// Pushes subscribed before passkeys stayed a device with no passkey.
	e.SQL(`INSERT INTO device (workspace_id, kind, name, push_endpoint, push_p256dh, push_auth) VALUES (1, 'browser', 'old phone', 'https://push.example/old', 'k', 'a')`)
	for _, q := range harness.BeforePasskeys {
		// A base of a deck whose devices kept their passkeys.
		e.SQL(q)
	}
	e = e.Restart()

	ds = browsers(t, e)
	if d := named(ds, "MacBook"); d.ID != m.ID || d.PasskeyID != m.ID || d.Passkey != "MacBook" {
		t.Fatalf("the device keeps its passkey, of the same id: %+v", ds)
	}
	if d := named(ds, "old phone"); d.ID == 0 || d.Passkey != "" || d.PasskeyID != 0 || !d.Push || d.Revoked {
		t.Fatalf("a device of pushes only stays one with no passkey: %+v", ds)
	}
	var on []browserDevice
	e.Signed(macCookie).Must("deck.devices_list", map[string]any{}, &on)
	if !named(on, "MacBook").This {
		t.Fatalf("the first browser to call keeps the device: %+v", on)
	}
	e.Signed(phoneCookie).Must("deck.devices_list", map[string]any{}, &on)
	i := slices.IndexFunc(on, func(d browserDevice) bool { return d.This })
	if i < 0 || on[i].ID == m.ID || on[i].PasskeyID != m.ID || len(on) != len(ds)+1 {
		t.Fatalf("the other browser gets a device of its own by the same passkey: %+v", on)
	}
	e.Signed(phoneCookie).Must("deck.devices_list", map[string]any{}, &on)
	if len(on) != len(ds)+1 {
		t.Fatalf("a session settles once: %+v", on)
	}
	if named(on, "MacBook").Push || on[i].Push {
		t.Fatalf("the pushes of a device two browsers shared stop, as either may have had them: %+v", on)
	}
	e.Must("deck.device_revoke", map[string]any{"device": on[i].ID}, nil)
	if code, _ := e.Signed(phoneCookie).Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("the phone's device is revoked alone: %d", code)
	}
	if code, out := e.Signed(macCookie).Call("deck.menu_get", map[string]any{}); code != 200 {
		t.Fatalf("the Mac stays signed in: %d %s", code, out)
	}
}

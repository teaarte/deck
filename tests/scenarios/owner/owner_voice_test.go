package owner

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"

	"golang.org/x/crypto/hkdf"
)

func TestVoiceBecomesTextOfTheMessage(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var got struct {
		file, format, language string
		size                   int
	}
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		mu.Lock()
		defer mu.Unlock()
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "file":
				got.file, got.size = p.FileName(), len(b)
			case "response_format":
				got.format = string(b)
			case "language":
				got.language = string(b)
			}
		}
		if got.size == 0 {
			http.Error(w, "no audio", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"text": " buy oat milk "}`))
	}))
	t.Cleanup(whisper.Close)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "Put it into groceries."}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "whisper", "model": "whisper", "kind": "stt", "name": "whisper", "free": true, "options": `{"language": "en"}`}, nil)
	id := e.Upload("master", "voice.wav", "audio/wav", "RIFF....WAVEfmt fake recording")
	var out struct{ Text string }
	e.Must("deck.transcribe", map[string]any{"file": harness.Itoa(id)}, &out)
	mu.Lock()
	if out.Text != "buy oat milk" || got.file != "voice.wav" || got.format != "json" || got.language != "en" {
		t.Fatalf("the recording goes to the whisper server as a form and its text comes back: %q %+v", out.Text, got)
	}
	mu.Unlock()
	if _, err := e.App.Store.Q.GetFile(context.Background(), id); err == nil {
		t.Fatal("the recording is not kept")
	}
	topic := e.Say(0, out.Text)
	if said := e.MasterSaid(topic, 1); said[0].Text != "Put it into groceries." {
		t.Fatalf("the text goes to the master as the owner's message: %+v", said)
	}
	var calls []harness.SpendRow
	e.ModelCalls("", 0, &calls)
	if len(calls) != 1 || calls[0].Provider != "whisper" || calls[0].Model != "whisper" {
		t.Fatalf("speech to text is a call of its model in the journal: %+v", calls)
	}
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": "http://127.0.0.1:1/inference", "local": true}, nil)
	id = e.Upload("master", "voice.wav", "audio/wav", "RIFF")
	if code, msg := e.Call("deck.transcribe", map[string]any{"file": harness.Itoa(id)}); code != 422 || !strings.Contains(string(msg), "speech to text") {
		t.Fatalf("a whisper that does not answer is an error the owner reads: %d %s", code, msg)
	}
}

// pushDevice is a browser subscription the fake push service delivers to: its keys and what reached it.
type pushDevice struct {
	priv *ecdh.PrivateKey
	auth []byte
	mu   sync.Mutex
	got  []map[string]string
	vap  []string
	gone bool
}

func newPushDevice(t *testing.T) (*pushDevice, *httptest.Server) {
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	d := &pushDevice{priv: priv, auth: make([]byte, 16)}
	_, _ = rand.Read(d.auth)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		d.vap = append(d.vap, r.Header.Get("Authorization"))
		var raw map[string]any
		_ = json.Unmarshal(d.decrypt(t, body), &raw)
		m := map[string]string{}
		for k, v := range raw {
			m[k] = fmt.Sprint(v)
		}
		d.got = append(d.got, m)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return d, srv
}

// decrypt opens an aes128gcm push the way a browser does (RFC 8291).
func (d *pushDevice) decrypt(t *testing.T, body []byte) []byte {
	salt, idlen := body[:16], int(body[20])
	sender, data := body[21:21+idlen], body[21+idlen:]
	pub, err := ecdh.P256().NewPublicKey(sender)
	if err != nil {
		t.Errorf("sender key: %v", err)
		return nil
	}
	shared, _ := d.priv.ECDH(pub)
	info := append(append([]byte("WebPush: info\x00"), d.priv.PublicKey().Bytes()...), sender...)
	ikm := make([]byte, 32)
	_, _ = io.ReadFull(hkdf.New(sha256.New, shared, d.auth, info), ikm)
	read := func(label string, n int) []byte {
		out := make([]byte, n)
		_, _ = io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: "+label+"\x00")), out)
		return out
	}
	block, _ := aes.NewCipher(read("aes128gcm", 16))
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, read("nonce", 12), data, nil)
	if err != nil {
		t.Errorf("open the push: %v", err)
		return nil
	}
	end := len(plain) - 1
	for end > 0 && plain[end] == 0 {
		end--
	}
	_ = binary.BigEndian
	return plain[:end]
}

func (d *pushDevice) subscribe(e *harness.Env, srv *httptest.Server, name string) {
	b64 := base64.RawURLEncoding.EncodeToString
	e.Must("deck.push_subscribe", map[string]any{"endpoint": srv.URL + "/push/" + name, "p256dh": b64(d.priv.PublicKey().Bytes()),
		"auth": b64(d.auth), "device": name}, nil)
}

// wait looks for what to push, as the loop of deck does every 30 seconds, until the device got n pushes.
func (d *pushDevice) wait(e *harness.Env, n int) []map[string]string {
	e.T.Helper()
	e.Until("pushes", func() bool {
		e.App.Sweep(context.Background(), time.Now())
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.got) >= n
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]string{}, d.got...)
}

// TestPushGoesToSubscribedDevices: a device gets what waits for the owner and the notes of the feed; a failure
// or a reply of the master pushes nothing of its own.
func TestPushGoesToSubscribedDevices(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "chat topic") {
			return provider.Result{Text: "Put it into groceries, as card 1."}
		}
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which site?\nThe first one is down."}}})
		return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
	}))

	var key struct{ Key string }
	e.Must("deck.push_key", nil, &key)
	d, srv := newPushDevice(t)
	d.subscribe(e, srv, "iPhone")
	var devices []struct{ Name string }
	e.Must("deck.push_devices", nil, &devices)
	if len(devices) != 1 || devices[0].Name != "iPhone" {
		t.Fatalf("the device is listed: %+v", devices)
	}
	p := e.Project("work", harness.PlainManifest)
	e.Project("private", harness.ManifestWith(harness.PlainManifest, `"no_network": true`))
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The report of the week is ready"}, nil)
	got := d.wait(e, 1)
	if got[0]["body"] != "The report of the week is ready" || got[0]["url"] != "/#/deck/cockpit" || got[0]["badge"] != "0" ||
		!strings.Contains(d.vap[0], "vapid t=") || !strings.Contains(d.vap[0], "k="+key.Key) {
		t.Fatalf("a notify line is pushed, signed with the key of deck: %+v %v", got, d.vap)
	}
	id := e.Run(p, "work")
	got = d.wait(e, 2)
	if got[1]["title"] != "Waiting for you" || got[1]["body"] != "Which site?" || got[1]["url"] != "/#/deck/cockpit" || got[1]["badge"] != "1" {
		t.Fatalf("a question is pushed by its first line: %+v", got[1])
	}
	e.Answer(e.WaitAsk().ID, "the second")
	e.WaitState(id, "error")
	topic := e.Say(0, "buy milk")
	e.MasterSaid(topic, 1)
	e.Must("deck.notify", map[string]any{"project": "private", "text": "Your blood test is in"}, nil)
	got = d.wait(e, 3)
	if len(got) != 3 || strings.Contains(got[2]["body"], "blood") || got[2]["body"] != "Something in deck waits for you" {
		t.Fatalf("a failure and a reply push nothing; a project without network pushes only a generic line: %+v", got)
	}
	long := strings.Repeat("word ", 60)
	e.Must("deck.notify", map[string]any{"project": "work", "text": long}, nil)
	got = d.wait(e, 4)
	if len(got[3]["body"]) > 104 {
		t.Fatalf("a push is short: %d bytes", len(got[3]["body"]))
	}
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The runs of today", "screen": "deck/schedule"}, nil)
	if got = d.wait(e, 5); got[4]["url"] != "/#/deck/schedule" {
		t.Fatalf("a note of a screen of deck pushes that screen: %+v", got[4])
	}
	e.Must("deck.notify", map[string]any{"project": "master", "text": "The blood test of private is in"}, nil)
	if got = d.wait(e, 6); got[5]["body"] != "Something in deck waits for you" {
		t.Fatalf("a line of the master, which may quote a project without network, is generic too: %+v", got[5])
	}
	d.mu.Lock()
	d.gone = true
	d.mu.Unlock()
	e.Must("deck.notify", map[string]any{"project": "work", "text": "once more"}, nil)
	e.Until("the gone device to go", func() bool {
		e.App.Sweep(context.Background(), time.Now())
		var ds []map[string]any
		e.Must("deck.push_devices", nil, &ds)
		return len(ds) == 0
	})
}

func TestAPushThatFailsForNowIsSentAgain(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "The list is updated."}
	}))

	d, srv := newPushDevice(t)
	failing := true
	var mu sync.Mutex
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		f := failing
		mu.Unlock()
		if f {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(flaky.Close)
	d.subscribe(e, flaky, "mac")
	e.Project("work", harness.PlainManifest)
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The list is updated."}, nil)
	e.App.Sweep(context.Background(), time.Now())
	if n := len(d.got); n != 0 {
		t.Fatalf("nothing arrives while the service fails: %d", n)
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	e.App.Sweep(context.Background(), time.Now())
	if got := d.wait(e, 1); got[0]["body"] != "The list is updated." {
		t.Fatalf("the push that failed for now goes out at the next look: %+v", got)
	}
}

// TestTheLanguageOfSpeechIsDetectedByDefault: the whisper.cpp deck sets up asks for auto, also before the
// owner set up speech at all; an OpenAI-style API, which detects it when the field is left out, gets none.
func TestTheLanguageOfSpeechIsDetectedByDefault(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	langs := map[string]string{}
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		lang := "-"
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == "language" {
				lang = string(b)
			}
		}
		mu.Lock()
		langs[r.URL.Path] = lang
		mu.Unlock()
		_, _ = w.Write([]byte(`{"text": "hi"}`))
	}))
	t.Cleanup(whisper.Close)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": whisper.URL + "/inference", "local": true}, nil)
	id := e.Upload("master", "voice.wav", "audio/wav", "RIFF....WAVEfmt fake recording")
	e.Must("deck.transcribe", map[string]any{"file": harness.Itoa(id)}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openai", "api": "openai", "url": whisper.URL + "/v1", "key": "sk-o"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openai", "model": "whisper-1", "kind": "stt"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "stt", "models": []string{"openai/whisper-1"}}, nil)
	id = e.Upload("master", "voice.wav", "audio/wav", "RIFF....WAVEfmt fake recording")
	e.Must("deck.transcribe", map[string]any{"file": harness.Itoa(id)}, nil)
	mu.Lock()
	defer mu.Unlock()
	if langs["/inference"] != "auto" || langs["/v1/audio/transcriptions"] != "-" {
		t.Fatalf("the whisper of deck detects the language, an OpenAI-style service is not told one: %v", langs)
	}
}

// TestThePushKeyIsDecksOwnAndResetsWithItsButton: the key of pushes is not among the owner's secrets; its reset
// makes a new key and drops the devices subscribed with the old one.
func TestThePushKeyIsDecksOwnAndResetsWithItsButton(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var before struct{ Key string }
	e.Must("deck.push_key", nil, &before)
	d, srv := newPushDevice(t)
	d.subscribe(e, srv, "iPhone")
	var names []struct{ Name string }
	e.Must("deck.secrets_list", nil, &names)
	if len(names) != 0 {
		t.Fatalf("the key of pushes is not a secret of the owner: %+v", names)
	}
	e.Must("deck.push_reset", nil, nil)
	var after struct{ Key string }
	e.Must("deck.push_key", nil, &after)
	var devices []struct{ Name string }
	e.Must("deck.push_devices", nil, &devices)
	if after.Key == before.Key || len(devices) != 0 {
		t.Fatalf("a reset makes a new key and the old devices subscribe again: %v %+v", after.Key == before.Key, devices)
	}
}

// TestNoToolGetsThePushKey: a tool that names deck's own key in its request does not get it.
func TestNoToolGetsThePushKey(t *testing.T) {
	t.Parallel()
	seen := make(chan string, 1)
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		seen <- r.Header.Get("Authorization")
		harness.JsonReply(w, `{}`)
	})
	e := harness.New(t)
	e.Must("deck.push_key", nil, nil)
	m := harness.Manifest(fmt.Sprintf(`{"allowed_external": ["post"],
  "tools": {"http": [{"name": "post", "description": "Posts", "method": "POST", "url": "%s/x", "headers": {"Authorization": "$secret.push.vapid"},
    "input": {"type": "object", "properties": {}}}]}}`, svc.URL))
	e.Project("spy", m)
	if code, out := e.Call("spy.post", map[string]any{}); code == 200 || !strings.Contains(string(out), "not set") {
		t.Fatalf("the push key reaches no tool: %d %s", code, out)
	}
	select {
	case got := <-seen:
		t.Fatalf("the request went out with %q", got)
	default:
	}
}

// TestADeviceThatFailsHoldsBackOnlyItself: each device keeps its own place in what was pushed, so a device whose
// service fails gets it later and the others get each push once; the health of pushes names the failing one.
func TestADeviceThatFailsHoldsBackOnlyItself(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	good, srv := newPushDevice(t)
	good.subscribe(e, srv, "iPhone")
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(broken.Close)
	good.subscribe(e, broken, "mac")
	e.Project("work", harness.PlainManifest)
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The report is ready"}, nil)
	good.wait(e, 1)
	for range 3 {
		e.App.Sweep(context.Background(), time.Now())
	}
	if n := len(good.wait(e, 1)); n != 1 {
		t.Fatalf("a device that took a push does not get it again while another fails: %d pushes", n)
	}
	if h := e.WaitHealth("push", "error"); !strings.Contains(h.Error, "mac") {
		t.Fatalf("the health of pushes names the device that fails: %+v", h)
	}
}

// pushDevices is the count of the devices that get pushes.
func pushDevices(e *harness.Env) int {
	var ds []map[string]any
	e.Must("deck.push_devices", nil, &ds)
	return len(ds)
}

// stoppedNotes is the count of the notes that pushes stopped.
func stoppedNotes(e *harness.Env) int {
	n := 0
	for _, f := range e.Notes() {
		if strings.HasPrefix(f.Text, "Pushes stopped") {
			n++
		}
	}
	return n
}

// TestADeviceThePushServiceRefusesIsTurnedOnAgainFromItsHealthRow: a refusal for good, such as 400, says how to turn
// the pushes of that device on again, and the row of the health opens where that is done.
func TestADeviceThePushServiceRefusesIsTurnedOnAgainFromItsHealthRow(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	dev, _ := newPushDevice(t)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"VapidPkHashMismatch"}`))
	}))
	t.Cleanup(refusing.Close)
	dev.subscribe(e, refusing, "MacIntel")
	e.Project("work", harness.PlainManifest)
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The report is ready"}, nil)
	e.App.Sweep(context.Background(), time.Now())
	h := e.WaitHealth("push", "error")
	if !strings.Contains(h.Error, "MacIntel") || !strings.Contains(h.Error, "on again on this device") {
		t.Fatalf("the health says how to turn the pushes of the device on again: %+v", h)
	}
	subs, notes := pushDevices(e), stoppedNotes(e)
	if subs != 0 || notes != 1 {
		t.Fatalf("a refused subscription is forgotten, and the owner gets one note: %d subscriptions, %d notes", subs, notes)
	}
	rows := 0
	for _, r := range e.Health() {
		if r.Subject == "push" {
			rows++
		}
	}
	if rows != 1 {
		t.Fatal("the devices are of a workspace, and so is the health of their pushes")
	}
	if a := e.Button("", "settings", map[string]any{"dialog": "checks"}, "", map[string]any{"tab": "Advanced"}); a["screen"] != "settings" {
		t.Fatalf("the row of the health opens the tab with the pushes: %+v", a)
	}
}

// TestThePushKeyLivesApartFromTheSecretsOfTheWorkspace: deck's key of pushes is outside the folder of secrets; the
// key of a base from before moves out unchanged, and a secret named like it is the owner's own.
func TestThePushKeyLivesApartFromTheSecretsOfTheWorkspace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	var first struct{ Key string }
	e.Must("deck.push_key", nil, &first)
	h := deckhome.Layout(home)
	old := filepath.Join(h.Secrets(1), "push.vapid")
	if _, err := os.Stat(old); err == nil {
		t.Fatal("the key of pushes is not in the folder of secrets")
	}
	keys, _ := filepath.Glob(filepath.Join(h.State(), "push", "*"))
	if len(keys) != 1 {
		t.Fatalf("the key of pushes lives in state/push: %v", keys)
	}
	var again struct{ Key string }
	e.Secret("", "push.vapid", "the owner's")
	e.Must("deck.push_key", nil, &again)
	if again.Key != first.Key {
		t.Fatal("a secret of the owner named like it does not change the key of pushes")
	}
}

// TestAPushRefusedForDecksOwnSignatureKeepsTheDevice: a refusal that is not of the subscription, such as a fault of
// deck's own signature, is red in health and forgets no device.
func TestAPushRefusedForDecksOwnSignatureKeepsTheDevice(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	dev, _ := newPushDevice(t)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"reason":"BadJwtToken"}`))
	}))
	t.Cleanup(refusing.Close)
	dev.subscribe(e, refusing, "MacIntel")
	e.Project("work", harness.PlainManifest)
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The report is ready"}, nil)
	e.App.Sweep(context.Background(), time.Now())
	if h := e.WaitHealth("push", "error"); !strings.Contains(h.Error, "BadJwtToken") {
		t.Fatalf("the health says why the service refused: %+v", h)
	}
	if subs := pushDevices(e); subs != 1 {
		t.Fatalf("the device stays subscribed: %d", subs)
	}
}

// TestPushesThatStopTellTheOwnerOnceAndTheHealthStandsAlone: a refusal for good notes the owner once while the
// health of pushes stays red, and the health turns red even when the note cannot be written.
func TestPushesThatStopTellTheOwnerOnceAndTheHealthStandsAlone(t *testing.T) {
	t.Parallel()
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"VapidPkHashMismatch"}`))
	}))
	t.Cleanup(refusing.Close)
	notes := stoppedNotes
	e := harness.New(t)
	e.Project("work", harness.PlainManifest)
	for i, name := range []string{"MacIntel", "iPad"} {
		dev, _ := newPushDevice(t)
		dev.subscribe(e, refusing, name)
		e.Must("deck.notify", map[string]any{"project": "work", "text": fmt.Sprintf("Report %d is ready", i)}, nil)
		e.App.Sweep(context.Background(), time.Now())
		if h := e.WaitHealth("push", "error"); !strings.Contains(h.Error, name) {
			t.Fatalf("the health names the device: %+v", h)
		}
	}
	if n := notes(e); n != 1 {
		t.Fatalf("the owner gets one note while the pushes stay stopped: %d", n)
	}

	e = harness.New(t)
	e.Project("work", harness.PlainManifest)
	dev, _ := newPushDevice(t)
	dev.subscribe(e, refusing, "MacIntel")
	e.Must("deck.notify", map[string]any{"project": "work", "text": "The report is ready"}, nil)
	// A base that refuses the note, as a full disk would.
	e.SQL(`CREATE TRIGGER no_notes BEFORE INSERT ON feed WHEN NEW.text LIKE 'Pushes stopped%' BEGIN SELECT RAISE(FAIL, 'no notes'); END`)
	e.App.Sweep(context.Background(), time.Now())
	if h := e.WaitHealth("push", "error"); !strings.Contains(h.Error, "MacIntel") || notes(e) != 0 {
		t.Fatalf("the health of pushes is written apart from the note: %+v", h)
	}
}

// TestAPageWithoutAMicrophoneLearnsWhereToRecord: a browser that gives no microphone on plain http is sent to the https
// address of deck on the tailnet once deck is on it, and to none before.
func TestAPageWithoutAMicrophoneLearnsWhereToRecord(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var at struct{ HTTPS string }
	e.Must("deck.secure_address", nil, &at)
	if at.HTTPS != "" {
		t.Fatalf("off the tailnet there is no secure address: %+v", at)
	}
	e.App.TailnetHealth("needs_login", "sign in at https://login.tailscale.com/a/1", "")
	e.Must("deck.secure_address", nil, &at)
	if at.HTTPS != "" {
		t.Fatalf("a sign-in link is no address of deck: %+v", at)
	}
	e.App.TailnetHealth("ok", "https://deck.tail1234.ts.net", "https://deck.tail1234.ts.net")
	e.Must("deck.secure_address", nil, &at)
	if at.HTTPS != "https://deck.tail1234.ts.net" {
		t.Fatalf("on the tailnet the page is sent to its https address: %+v", at)
	}
}

package scenarios

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
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"

	"deck2/internal/provider"
)

func TestVoiceBecomesTextOfTheMessage(t *testing.T) {
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
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "Put it into groceries."}
	})
	e.must("deck.stt_set", map[string]any{"url": whisper.URL + "/inference", "language": "en"}, nil)
	id := e.upload("master", "voice.wav", "audio/wav", "RIFF....WAVEfmt fake recording")
	var out struct{ Text string }
	e.must("deck.transcribe", map[string]any{"file": itoa(id)}, &out)
	mu.Lock()
	if out.Text != "buy oat milk" || got.file != "voice.wav" || got.format != "json" || got.language != "en" {
		t.Fatalf("the recording goes to the whisper server as a form and its text comes back: %q %+v", out.Text, got)
	}
	mu.Unlock()
	if _, err := e.App.Store.Q.GetFile(context.Background(), id); err == nil {
		t.Fatal("the recording is not kept")
	}
	topic := e.say(0, out.Text)
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into groceries." {
		t.Fatalf("the text goes to the master as the owner's message: %+v", said)
	}
	e.must("deck.stt_set", map[string]any{"url": "http://127.0.0.1:1/inference"}, nil)
	id = e.upload("master", "voice.wav", "audio/wav", "RIFF")
	if code, msg := e.call("deck.transcribe", map[string]any{"file": itoa(id)}); code != 422 || !strings.Contains(string(msg), "speech to text") {
		t.Fatalf("a whisper that does not answer is an error the owner reads: %d %s", code, msg)
	}
	var health []struct{ Subject, Status string }
	e.must("deck.health_list", nil, &health)
	found := false
	for _, h := range health {
		found = found || h.Subject == "stt" && h.Status == "error"
	}
	if !found {
		t.Fatalf("the provider shows in health: %+v", health)
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
		var m map[string]string
		_ = json.Unmarshal(d.decrypt(t, body), &m)
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

func (d *pushDevice) subscribe(e *Env, srv *httptest.Server, name string) {
	b64 := base64.RawURLEncoding.EncodeToString
	e.must("deck.push_subscribe", map[string]any{"endpoint": srv.URL + "/push/" + name, "p256dh": b64(d.priv.PublicKey().Bytes()),
		"auth": b64(d.auth), "device": name}, nil)
}

func (d *pushDevice) wait(e *Env, n int) []map[string]string {
	e.T.Helper()
	e.waitFor("pushes", func() bool { d.mu.Lock(); defer d.mu.Unlock(); return len(d.got) >= n })
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]string{}, d.got...)
}

func TestPushGoesToSubscribedDevices(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "chat topic") {
			return provider.Result{Text: "Put it into groceries, as card 1."}
		}
		return provider.Result{IsError: true, Text: "the site is down"}
	})
	var key struct{ Key string }
	e.must("deck.push_key", nil, &key)
	d, srv := newPushDevice(t)
	d.subscribe(e, srv, "iPhone")
	var devices []struct{ Name string }
	e.must("deck.push_devices", nil, &devices)
	if len(devices) != 1 || devices[0].Name != "iPhone" {
		t.Fatalf("the device is listed: %+v", devices)
	}
	p := e.createProject("work", boardWork)
	e.createProject("private", strings.Replace(boardWork, `"version": 4,`, `"version": 4, "no_network": true,`, 1))
	time.Sleep(700 * time.Millisecond)
	e.must("deck.notify", map[string]any{"project": "work", "text": "The report of the week is ready"}, nil)
	got := d.wait(e, 1)
	if got[0]["body"] != "The report of the week is ready" || got[0]["url"] != "/#/deck/cockpit" ||
		!strings.Contains(d.vap[0], "vapid t=") || !strings.Contains(d.vap[0], "k="+key.Key) {
		t.Fatalf("a notify line is pushed, signed with the key of deck: %+v %v", got, d.vap)
	}
	e.waitState(e.run(p, "work"), "error")
	got = d.wait(e, 2)
	if got[1]["title"] != "work.work failed" || !strings.Contains(got[1]["body"], "the site is down") || !strings.HasPrefix(got[1]["url"], "/#/deck/process/") {
		t.Fatalf("a failure is pushed with the page of its process: %+v", got[1])
	}
	e.must("deck.notify", map[string]any{"project": "private", "text": "Your blood test is in"}, nil)
	got = d.wait(e, 3)
	if strings.Contains(got[2]["body"], "blood") || got[2]["body"] != "Something in deck waits for you" {
		t.Fatalf("a project without network pushes only a generic line: %+v", got[2])
	}
	topic := e.say(0, "buy milk")
	e.masterSaid(topic, 1)
	got = d.wait(e, 4)
	if got[3]["title"] != "Master" || got[3]["body"] != "The master replied" || got[3]["url"] != "/#/deck/chat" {
		t.Fatalf("the master's reply is pushed while no client is open, as a bare line while a project keeps off the network: %+v", got[3])
	}
	ctx, cancel := context.WithCancel(context.Background())
	_ = e.sse(ctx)
	e.say(topic, "and bread")
	e.masterSaid(topic, 2)
	time.Sleep(time.Second)
	if n := len(d.wait(e, 4)); n != 4 {
		t.Fatalf("with a client open the reply is not pushed: %d pushes", n)
	}
	cancel()
	long := strings.Repeat("word ", 60)
	e.must("deck.notify", map[string]any{"project": "work", "text": long}, nil)
	got = d.wait(e, 5)
	if len(got[4]["body"]) > 104 {
		t.Fatalf("a push is short: %d bytes", len(got[4]["body"]))
	}
	d.mu.Lock()
	d.gone = true
	d.mu.Unlock()
	e.must("deck.notify", map[string]any{"project": "work", "text": "once more"}, nil)
	e.waitFor("the gone device to go", func() bool {
		var ds []map[string]any
		e.must("deck.push_devices", nil, &ds)
		return len(ds) == 0
	})
}

func TestAPushThatFailsForNowIsSentAgain(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "The list is updated."}
	})
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
	e.createProject("work", boardWork)
	topic := e.say(0, "update the list")
	e.masterSaid(topic, 1)
	time.Sleep(time.Second)
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

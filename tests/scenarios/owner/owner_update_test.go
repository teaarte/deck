package owner

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"deck2/internal/app"
	"deck2/internal/app/updates"
	"deck2/internal/provider"
	"deck2/internal/release"
	"deck2/tests/harness"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRelease is the latest release of deck on an https server of its own, signed with a key of the test.
type fakeRelease struct {
	srv   *httptest.Server
	key   ed25519.PrivateKey
	mu    sync.Mutex
	files map[string][]byte
}

func newRelease(t *testing.T) *fakeRelease {
	harness.TrustTestTLS()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	r := &fakeRelease{key: key, files: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		b, ok := r.files[strings.TrimPrefix(req.URL.Path, "/latest/")]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// publish makes version the latest release with its files; lie changes the list after it is signed.
func (r *fakeRelease) publish(t *testing.T, version string, files map[string][]byte, lie func(*release.Files)) {
	fs := release.Files{Version: version}
	for name, b := range files {
		sum := sha256.Sum256(b)
		fs.Files = append(fs.Files, release.File{Name: name, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])})
	}
	list, sig, err := release.Sign(r.key, fs)
	if err != nil {
		t.Fatal(err)
	}
	if lie != nil {
		lie(&fs)
		list, _ = json.MarshalIndent(fs, "", "  ")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files = map[string][]byte{release.List: list, release.Signature: sig}
	for name, b := range files {
		r.files[name] = b
	}
}

// source is where the deck of a test finds the fake release.
func (r *fakeRelease) source() updates.Source {
	return updates.Source{URL: r.srv.URL + "/latest/", Domains: []string{strings.TrimPrefix(r.srv.URL, "https://")},
		Key: r.key.Public().(ed25519.PublicKey)}
}

// newBinary is a binary of deck of a version, as -version answers.
func newBinary(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + " abc deck-test\n")
}

// updateEnv is a deck of v0.1.0 whose binary is a file of the test, updated from the fake release.
func updateEnv(t *testing.T, r *fakeRelease, launcher bool, script harness.Script) (*harness.Env, string, chan release.Restart) {
	exe := filepath.Join(t.TempDir(), "deck")
	if err := os.WriteFile(exe, newBinary("v0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarts := make(chan release.Restart, 1)
	e := harness.New(t, harness.WithAgent(script), harness.WithConfig(func(c *app.Config) {
		c.Releases, c.Version, c.Variant, c.Launcher = r.source(), "v0.1.0", "deck-test", launcher
		c.Exe = func() (string, error) { return exe, nil }
		c.Restart = func(rs release.Restart) { restarts <- rs }
	}))

	return e, exe, restarts
}

// A deck finds a newer release, tells the owner once with a button, and the button puts the binary of its build in
// place and restarts deck in it, the old one kept until the new one runs.
func TestDeckUpdatesItselfFromASignedRelease(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	r.publish(t, "v0.2.0", map[string][]byte{"deck-test": newBinary("v0.2.0"), "deck-other": []byte("other")}, nil)
	e, exe, restarts := updateEnv(t, r, false, nil)
	var v struct {
		Version, Latest string
		Newer           bool
	}
	e.Must("deck.update_check", nil, &v)
	e.Must("deck.update_check", nil, &v)
	if v.Version != "v0.1.0" || v.Latest != "v0.2.0" || !v.Newer {
		t.Fatalf("the deck sees the newer release: %+v", v)
	}
	if h := e.WaitHealth(updates.Subject, "warning"); !strings.Contains(h.Error, "v0.2.0") {
		t.Fatalf("the health of the update names the new version: %+v", h)
	}
	n := 0
	for _, f := range e.Notes() {
		if strings.Contains(f.Text, "v0.2.0") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one note of the new version, however often deck looks: %d", n)
	}
	var feed []struct {
		Text    string
		Buttons []struct{ Label, Command string }
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note", "lang": "ru"}, &feed)
	var button struct{ Label, Command string }
	for _, f := range feed {
		if strings.Contains(f.Text, "v0.2.0") && len(f.Buttons) == 1 {
			button = f.Buttons[0]
		}
	}
	if button.Command != "deck.update" || button.Label != "Обновить" {
		t.Fatalf("the note's button is deck's update in the owner's words: %+v %+v", button, feed)
	}
	e.Must(button.Command, nil, nil)
	rs := <-restarts
	exe, _ = filepath.EvalSymlinks(exe)
	got, _ := os.ReadFile(exe)
	old, _ := os.ReadFile(rs.Old)
	p, pending := release.ReadPending(filepath.Join(e.App.Cfg.Home, "state", "update.json"))
	if !pending || p.Version != "v0.2.0" || p.Old != rs.Old {
		t.Fatalf("the update waits to be served, the old binary named: %+v", p)
	}
	if rs.Launcher || rs.Exe != exe || string(got) != string(newBinary("v0.2.0")) || string(old) != string(newBinary("v0.1.0")) {
		t.Fatalf("the new binary stands in place of the old, which is kept: %+v %q %q", rs, got, old)
	}
}

// A release whose signature does not hold, or whose file is not the one of the signed list, leaves deck as it is.
func TestDeckRefusesAReleaseThatIsNotSigned(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		lie    func(*release.Files)
		served []byte
	}{
		"signature": {lie: func(fs *release.Files) { fs.Files[0].Size++ }},
		"sha256":    {served: []byte("#!/bin/sh\necho v0.2.0 evil\n")},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRelease(t)
			r.publish(t, "v0.2.0", map[string][]byte{"deck-test": newBinary("v0.2.0")}, tc.lie)
			if tc.served != nil {
				r.mu.Lock()
				r.files["deck-test"] = tc.served
				r.mu.Unlock()
			}
			e, exe, restarts := updateEnv(t, r, false, nil)
			if status, body := e.Call("deck.update", nil); status < 400 {
				t.Fatalf("the update is refused: %d %s", status, body)
			}
			got, _ := os.ReadFile(exe)
			left, _ := filepath.Glob(exe + ".*")
			if string(got) != string(newBinary("v0.1.0")) || len(left) > 0 || len(restarts) > 0 {
				t.Fatalf("deck stays as it was, nothing left beside it: %q %v", got, left)
			}
		})
	}
}

// A deck its launcher runs asks the launcher to update it and replaces nothing itself.
func TestDeckOfALauncherAsksTheLauncher(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	r.publish(t, "v0.2.0", map[string][]byte{release.MacApp: []byte("zip")}, nil)
	e, exe, restarts := updateEnv(t, r, true, nil)
	e.Must("deck.update", nil, nil)
	if rs := <-restarts; !rs.Launcher {
		t.Fatalf("the launcher is asked: %+v", rs)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBinary("v0.1.0")) {
		t.Fatalf("deck replaced nothing itself: %q", got)
	}
}

// Only the owner updates deck: neither the master nor an agent of a module may.
func TestOnlyTheOwnerUpdatesDeck(t *testing.T) {
	t.Parallel()
	r := newRelease(t)
	r.publish(t, "v0.2.0", map[string][]byte{"deck-test": newBinary("v0.2.0")}, nil)
	said := make(chan string, 2)
	e, exe, _ := updateEnv(t, r, false, func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad := c.Tool(ctx, "update", map[string]any{})
		if !bad {
			out = "done: " + out
		}
		said <- out
		return provider.Result{Text: "tried"}
	})
	e.Say(0, "update deck")
	p := e.Project("plain", harness.PlainManifest)
	e.Run(p, "work")
	for range 2 {
		if out := <-said; strings.HasPrefix(out, "done: ") {
			t.Fatalf("an agent may not update deck: %s", out)
		}
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBinary("v0.1.0")) {
		t.Fatalf("deck stays as it was: %q", got)
	}
}

package owner

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestABigBodyOfSigningInIsRefused: a step of signing in is a few kilobytes; a megabyte is refused unread.
func TestABigBodyOfSigningInIsRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	u, _ := url.Parse(e.Here)
	for _, n := range []int{1 << 10, 1 << 20} {
		req, _ := http.NewRequest("POST", e.URL+"/auth/login/begin", strings.NewReader(`{"name": "`+strings.Repeat("x", n)+`"}`))
		req.Host = u.Host
		req.Header.Set("Origin", e.Here)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if big := n > 64<<10; big != (resp.StatusCode == http.StatusRequestEntityTooLarge) {
			t.Fatalf("a step of %d bytes: %d", n, resp.StatusCode)
		}
	}
}

// pictures writes n copies of a w×h picture in colour into the data folder of a new module and answers their names.
func pictures(t *testing.T, e *harness.Env, module string, w, h, n int) []string {
	t.Helper()
	p := e.Project(module, harness.ScriptManifest("collect", "", ""))
	dir := e.App.Home.Data(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	var names []string
	for i := range n {
		name := fmt.Sprintf("%d.png", i)
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// TestAHugePictureIsItsOwnLightCopy: a picture past 25 million pixels gets no light copy; deck gives it itself.
func TestAHugePictureIsItsOwnLightCopy(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	names := pictures(t, e, "tools", 6000, 5000, 1)
	code, body := e.Do("GET", "/api/files/data/tools/"+names[0]+"?thumb=1", nil)
	if code != 200 || !bytes.HasPrefix(body, []byte("\x89PNG")) {
		t.Fatalf("a picture of 30 million pixels is given itself: %d %q", code, body[:min(8, len(body))])
	}
	names = pictures(t, e, "small", 1000, 800, 1)
	if code, body := e.Do("GET", "/api/files/data/small/"+names[0]+"?thumb=1", nil); code != 200 || !bytes.HasPrefix(body, []byte("\xff\xd8")) {
		t.Fatalf("a smaller picture gets its light copy: %d", code)
	}
}

// TestLightCopiesOfBigPicturesAreMadeTwoAtATime: eight big pictures asked for at once do not all decode at once.
func TestLightCopiesOfBigPicturesAreMadeTwoAtATime(t *testing.T) {
	e := harness.New(t)
	names := pictures(t, e, "tools", 2800, 2800, 8)
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base, peak := ms.HeapInuse, ms.HeapInuse
	stop := make(chan struct{})
	var watched sync.WaitGroup
	watched.Go(func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapInuse)
		}
	})
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Go(func() {
			if code, body := e.Do("GET", "/api/files/data/tools/"+n+"?thumb=1", nil); code != 200 || !bytes.HasPrefix(body, []byte("\xff\xd8")) {
				t.Errorf("a light copy of %s: %d", n, code)
			}
		})
	}
	wg.Wait()
	close(stop)
	watched.Wait()
	if grew := (peak - base) >> 20; grew > 400 {
		t.Fatalf("eight pictures of 31 MB each decoded at once: the heap grew by %d MB", grew)
	}
}

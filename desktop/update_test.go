package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTheLauncherHoldsTheKeyOfDeck: the launcher checks releases with the public key deck holds.
func TestTheLauncherHoldsTheKeyOfDeck(t *testing.T) {
	deck, err := os.ReadFile("../internal/release/release.pub")
	if err != nil || strings.TrimSpace(string(deck)) != strings.TrimSpace(releaseKey) || len(Latest().Key) != ed25519.PublicKeySize {
		t.Fatalf("the key of the launcher is the key of deck: %v", err)
	}
}

// appZip is deck.app of a release in a zip, its deck saying what.
func appZip(t *testing.T, says string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "deck.app/Contents/MacOS/deck", Method: zip.Deflate}
	h.SetMode(0o755)
	w, _ := z.CreateHeader(h)
	_, _ = w.Write([]byte(says))
	w, _ = z.Create("deck.app/Contents/Info.plist")
	_, _ = w.Write([]byte("<plist/>"))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// release serves a release of one file, signed by key, or by another key when forged.
func serveRelease(t *testing.T, name string, file []byte, forged bool) Release {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	sign := key
	if forged {
		_, sign, _ = ed25519.GenerateKey(rand.Reader)
	}
	sum := sha256.Sum256(file)
	list, _ := json.Marshal(map[string]any{"version": "v0.2.0", "files": []map[string]any{{"name": name, "size": len(file), "sha256": hex.EncodeToString(sum[:])}}})
	files := map[string][]byte{"release.json": list, "release.json.sig": []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(sign, list))), name: file}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return Release{URL: srv.URL + "/", Key: pub, Client: srv.Client()}
}

// oldApp is deck.app as it stands before an update.
func oldApp(t *testing.T) string {
	bundle := filepath.Join(t.TempDir(), "deck.app")
	_ = os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755)
	_ = os.WriteFile(filepath.Join(bundle, "Contents", "MacOS", "deck"), []byte("old"), 0o755)
	return bundle
}

// TestTheLauncherPutsTheNewAppInPlace: the deck.app of a signed release takes the place of the old one whole.
func TestTheLauncherPutsTheNewAppInPlace(t *testing.T) {
	r := serveRelease(t, "deck-macos.zip", appZip(t, "new"), false)
	bundle := oldApp(t)
	if err := ReplaceApp(context.Background(), r, bundle); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "deck"))
	st, _ := os.Stat(filepath.Join(bundle, "Contents", "MacOS", "deck"))
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(bundle), ".deck-update-*"))
	if string(got) != "new" || st.Mode()&0o111 == 0 || len(left) > 0 {
		t.Fatalf("the new app runs in place of the old, nothing left beside it: %q %v %v", got, st.Mode(), left)
	}
}

// TestTheLauncherRefusesAForgedRelease: a release not signed by deck's key leaves the app as it is.
func TestTheLauncherRefusesAForgedRelease(t *testing.T) {
	r := serveRelease(t, "deck-macos.zip", appZip(t, "evil"), true)
	bundle := oldApp(t)
	if err := ReplaceApp(context.Background(), r, bundle); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a forged release is refused: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(bundle, "Contents", "MacOS", "deck")); string(got) != "old" {
		t.Fatalf("the app stays as it was: %q", got)
	}
}

// TestDeckAsksTheLauncherForItsUpdate: deck that ends with the code of an update makes the launcher update it.
func TestDeckAsksTheLauncherForItsUpdate(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "deck")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\nexit 75\n"), 0o755)
	asked := make(chan struct{}, 1)
	d := &Deck{Runner: Local{Bin: bin, Home: t.TempDir()}, Addr: freeAddr(t), Update: func() { asked <- struct{}{} }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = d.Start(ctx)
	select {
	case <-asked:
	case <-ctx.Done():
		t.Fatal("the launcher was not asked to update deck")
	}
}

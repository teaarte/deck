package main

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Releases is the folder of the files of the latest release of deck.
const Releases = "https://github.com/teaarte/deck/releases/latest/download/"

// UpdateCode is the exit code with which deck asks the launcher to update it.
const UpdateCode = 75

// releaseKey is the public key the list of a release is signed with, the one deck holds.
//
//go:embed release.pub
var releaseKey string

// Release is where the launcher takes an update from: the folder of the latest release's files and its key.
type Release struct {
	URL    string
	Key    ed25519.PublicKey
	Client *http.Client
}

// Latest is the release of deck's own repository.
func Latest() Release {
	k, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(releaseKey))
	return Release{URL: Releases, Key: k, Client: &http.Client{Timeout: 30 * time.Minute}}
}

type releaseFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// list is the signed list of the latest release.
func (r Release) list(ctx context.Context) (version string, files []releaseFile, err error) {
	list, err := r.get(ctx, "release.json", 1<<20)
	if err != nil {
		return "", nil, err
	}
	sig, err := r.get(ctx, "release.json.sig", 1<<10)
	if err != nil {
		return "", nil, err
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(r.Key) != ed25519.PublicKeySize || !ed25519.Verify(r.Key, list, s) {
		return "", nil, errors.New("the signature of the release does not hold")
	}
	var fs struct {
		Version string        `json:"version"`
		Files   []releaseFile `json:"files"`
	}
	if err := json.Unmarshal(list, &fs); err != nil {
		return "", nil, err
	}
	return fs.Version, fs.Files, nil
}

func (r Release) get(ctx context.Context, name string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+name, nil)
	res, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s of the latest release: %s", name, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

// Fetch writes the file of a name of the latest release to path once its size and sha256 are those of the signed
// list; it gives the version of the release.
func (r Release) Fetch(ctx context.Context, name, path string) (string, error) {
	version, files, err := r.list(ctx)
	if err != nil {
		return "", err
	}
	var f *releaseFile
	for i := range files {
		if files[i].Name == name {
			f = &files[i]
		}
	}
	if f == nil {
		return "", fmt.Errorf("release %s has no %s", version, name)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+name, nil)
	res, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", name, res.Status)
	}
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(res.Body, f.Size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256) {
		err = fmt.Errorf("%s is not the file of the signed list", name)
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return version, nil
}

// ReplaceApp puts the deck.app of the latest release in place of bundle; the old one goes once the new one stands.
func ReplaceApp(ctx context.Context, r Release, bundle string) error {
	parent := filepath.Dir(bundle)
	work, err := os.MkdirTemp(parent, ".deck-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	archive := filepath.Join(work, "deck-macos.zip")
	if _, err := r.Fetch(ctx, "deck-macos.zip", archive); err != nil {
		return err
	}
	if err := unzip(archive, work); err != nil {
		return err
	}
	fresh := filepath.Join(work, "deck.app")
	if st, err := os.Stat(filepath.Join(fresh, "Contents", "MacOS", "deck")); err != nil || st.Mode()&0o111 == 0 {
		return errors.New("the release holds no deck.app")
	}
	old := filepath.Join(work, "old.app")
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return err
	}
	return nil
}

// unzip unpacks an archive into dir, refusing a path that leaves it.
func unzip(archive, dir string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		path := filepath.Join(dir, f.Name)
		if !filepath.IsLocal(f.Name) {
			return fmt.Errorf("the archive names %q outside its folder", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the archive holds a link %q", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, f.Mode().Perm()|0o600)
		if err == nil {
			_, err = io.Copy(out, in)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

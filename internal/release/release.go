// Package release is what a release of deck is: the version and variant of this build, and the list of a release's
// files with their sizes and sha256, signed with ed25519.
package release

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Version and Commit are those of the release this build is, set by -ldflags -X; a build from source is dev.
var (
	Version = "dev"
	Commit  = ""
)

// The files of a release that are not a binary of a variant.
const (
	// List is the list of the files; Signature its ed25519 signature in base64.
	List      = "release.json"
	Signature = "release.json.sig"
	// MacApp is deck.app for macOS in a zip; Setup the installer for Windows.
	MacApp = "deck-macos.zip"
	Setup  = "deck-setup.exe"
)

//go:embed release.pub
var publicKey string

// Key is the public key releases are signed with.
func Key() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("release: the public key is not an ed25519 key in base64")
	}
	return b
}

// Variant is the name of the binary of this build in a release: deck-<os>-<arch>, -tailnet with the tailnet.
func Variant(tailnet bool) string {
	v := "deck-" + runtime.GOOS + "-" + runtime.GOARCH
	if tailnet {
		v += "-tailnet"
	}
	return v
}

// File is a file of a release.
type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Files is the signed list of a release.
type Files struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Files   []File `json:"files"`
}

// File is the file of a name, if the release has it.
func (fs Files) File(name string) (File, bool) {
	for _, f := range fs.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}

// Sign is the list as it is published and its signature.
func Sign(key ed25519.PrivateKey, fs Files) (list, sig []byte, err error) {
	if !valid(fs.Version) {
		return nil, nil, fmt.Errorf("release: %q is not a version vMAJOR.MINOR.PATCH", fs.Version)
	}
	if list, err = json.MarshalIndent(fs, "", "  "); err != nil {
		return nil, nil, err
	}
	return list, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, list)) + "\n"), nil
}

// Read is the list of a release once its signature by key holds.
func Read(key ed25519.PublicKey, list, sig []byte) (Files, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(key, list, s) {
		return Files{}, errors.New("the signature of the release does not hold")
	}
	var fs Files
	if err := json.Unmarshal(list, &fs); err != nil {
		return Files{}, fmt.Errorf("the list of the release: %w", err)
	}
	if !valid(fs.Version) {
		return Files{}, fmt.Errorf("the release names no version: %q", fs.Version)
	}
	return fs, nil
}

// Newer reports whether version a is later than b; a build of dev is never older or newer.
func Newer(a, b string) bool {
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func valid(v string) bool { _, ok := parse(v); return ok }

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Restart is how deck ends to take an update: in place, its binary at Exe new and the old one kept at Old until the
// new one runs, or by its launcher, which updates what it runs.
type Restart struct {
	Launcher bool
	Exe, Old string
}

// LauncherCode is the exit code with which deck asks its launcher to update it.
const LauncherCode = 75

// Pending is an update put in place whose deck has not served yet: a start that finds it tried starts the old binary.
type Pending struct {
	Exe     string `json:"exe"`
	Old     string `json:"old"`
	Version string `json:"version"`
	Tried   bool   `json:"tried,omitempty"`
}

// ReadPending is the pending update kept at path, if there is one.
func ReadPending(path string) (Pending, bool) {
	b, err := os.ReadFile(path)
	var p Pending
	if err != nil || json.Unmarshal(b, &p) != nil || p.Exe == "" || p.Old == "" {
		return Pending{}, false
	}
	return p, true
}

// WritePending keeps a pending update at path.
func WritePending(path string, p Pending) error {
	b, _ := json.Marshal(p)
	return os.WriteFile(path, b, 0o600)
}

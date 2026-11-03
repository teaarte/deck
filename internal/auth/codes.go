package auth

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"deck2/internal/home"
	"deck2/internal/nonce"
)

// Times and tries of a code that pairs a device.
const (
	CodeLife  = 10 * time.Minute
	codeTries = 5
)

// code is a file of the folder of codes: the sha256 of the code, until when it pairs, what device it pairs and how
// many ceremonies it began.
type code struct {
	Hash      string    `json:"hash"`
	Until     time.Time `json:"until"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Workspace int64     `json:"workspace"`
	Tries     int       `json:"tries"`
	file      string
}

// Code makes a code that pairs one device of kind in the workspace for ten minutes; deck pair writes the same file.
func (a *Auth) Code(workspace int64, kind, name string) (string, time.Time, error) {
	switch kind {
	case Browser, Launcher, Node:
	default:
		return "", time.Time{}, errors.New("deck: a device is a browser, the launcher or a node")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, until := nonce.Token(), a.cfg.Now().Add(CodeLife)
	c := code{Hash: hash(raw), Until: until, Kind: kind, Name: name, Workspace: workspace}
	if err := os.MkdirAll(a.cfg.Home.Pair(), 0o700); err != nil {
		return "", time.Time{}, err
	}
	b, _ := json.Marshal(c)
	return raw, until, home.WriteFile(filepath.Join(a.cfg.Home.Pair(), nonce.Hex(8)), b)
}

// take finds the code of raw and counts a try of it; a code past its time, or past its tries for a new try, is gone.
// It runs under a.mu.
func (a *Auth) take(raw string, try bool) (code, error) {
	entries, err := os.ReadDir(a.cfg.Home.Pair())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return code{}, err
	}
	want, now := hash(raw), a.cfg.Now()
	for _, e := range entries {
		path := filepath.Join(a.cfg.Home.Pair(), e.Name())
		b, err := os.ReadFile(path)
		var c code
		if err != nil || json.Unmarshal(b, &c) != nil || now.After(c.Until) || try && c.Tries >= codeTries {
			_ = os.Remove(path)
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(want)) != 1 {
			continue
		}
		c.file = path
		if try {
			c.Tries++
			b, _ := json.Marshal(c)
			if err := home.WriteFile(path, b); err != nil {
				return code{}, err
			}
		}
		return c, nil
	}
	return code{}, ErrSignIn
}

// spend ends a code once its device is paired; a second use finds nothing.
func (a *Auth) spend(c code) error {
	if err := os.Remove(c.file); err != nil {
		return ErrSignIn
	}
	return nil
}

// Command deck-release writes the signed list of a release: release.json with the name, size and sha256 of each file
// given, and release.json.sig, signed by the ed25519 seed in base64 of DECK_RELEASE_KEY.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/release"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "deck-release:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "the version of the release, such as v0.1.0")
	commit := flag.String("commit", "", "the commit it is built from")
	out := flag.String("out", ".", "the folder release.json and its signature go to")
	flag.Parse()
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("DECK_RELEASE_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("DECK_RELEASE_KEY is not an ed25519 seed in base64")
	}
	key := ed25519.NewKeyFromSeed(seed)
	if !key.Public().(ed25519.PublicKey).Equal(release.Key()) {
		return errors.New("DECK_RELEASE_KEY is not the key whose public half deck holds")
	}
	fs := release.Files{Version: *version, Commit: *commit}
	for _, path := range flag.Args() {
		f, err := describe(path)
		if err != nil {
			return err
		}
		fs.Files = append(fs.Files, f)
	}
	list, sig, err := release.Sign(key, fs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, release.List), list, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, release.Signature), sig, 0o644)
}

func describe(path string) (release.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return release.File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return release.File{}, err
	}
	return release.File{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

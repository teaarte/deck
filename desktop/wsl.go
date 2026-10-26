package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// WSL is the deck of a WSL distribution of its own, as on Windows: deck runs inside as the user deck, and Windows
// reaches it at the same address on localhost.
type WSL struct {
	// Exe is wsl.exe; Distro the distribution of deck.
	Exe    string
	Distro string
}

// Bin is where deck stands inside the distribution.
const Bin = "/usr/local/bin/deck"

func (w WSL) run(user string, args ...string) *exec.Cmd {
	all := []string{"-d", w.Distro}
	if user != "" {
		all = append(all, "-u", user)
	}
	cmd := exec.Command(w.Exe, append(append(all, "--cd", "~", "--exec"), args...)...)
	hide(cmd)
	return cmd
}

func (w WSL) Command(addr string) *exec.Cmd { return w.run("", Bin, "-addr", addr) }

func (w WSL) Token(ctx context.Context) (string, error) {
	out, err := w.run("", "sh", "-c", `cat "${DECK_HOME:-$HOME/.deck2}/state/token"`).Output()
	return string(out), err
}

func (w WSL) Stop(*exec.Cmd) error { return w.run("", "pkill", "-TERM", "-x", "deck").Run() }

// provision readies a fresh distribution for deck: bubblewrap for its sandbox, the user deck it runs as by default,
// and no first-run questions of the distribution.
const provision = `set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q bubblewrap ca-certificates curl git python3
id deck >/dev/null 2>&1 || useradd -m -s /bin/bash deck
printf '[user]\ndefault=deck\n' > /etc/wsl.conf
rm -f /etc/wsl-distribution.conf.d/oobe.conf
sed -i '/^\[oobe\]/,/^\[/{/^command/d}' /etc/wsl-distribution.conf 2>/dev/null || true`

// Setup makes deck ready in WSL: WSL itself, the distribution of deck, its packages and the deck binary bin, a path
// on Windows; say tells each step. It is safe to run again, as an update does.
func (w WSL) Setup(bin string, say func(string)) error {
	if err := exec.Command(w.Exe, "--status").Run(); err != nil {
		say(tr("Installing WSL; Windows asks for consent and may need a restart."))
		if out, err := exec.Command(w.Exe, "--install", "--no-distribution").CombinedOutput(); err != nil {
			return fmt.Errorf("install WSL: %v: %s", err, wide(out))
		}
		return errRestart
	}
	if !w.exists() {
		say(tr("Installing Ubuntu for deck as the distribution deck."))
		if out, err := exec.Command(w.Exe, "--install", "Ubuntu-24.04", "--name", w.Distro, "--no-launch").CombinedOutput(); err != nil {
			return fmt.Errorf("install the distribution: %v: %s", err, wide(out))
		}
	}
	say(tr("Preparing the distribution: bubblewrap and the user deck."))
	if out, err := w.run("root", "sh", "-c", provision).CombinedOutput(); err != nil {
		return fmt.Errorf("prepare the distribution: %v: %s", err, out)
	}
	say(tr("Putting deck into the distribution."))
	_ = w.Stop(nil)
	if out, err := w.run("root", "sh", "-c", `install -m 0755 "$(wslpath -u "$0")" `+Bin, bin).CombinedOutput(); err != nil {
		return fmt.Errorf("put deck in: %v: %s", err, out)
	}
	_ = exec.Command(w.Exe, "--terminate", w.Distro).Run()
	return nil
}

// errRestart says WSL was installed and Windows needs a restart before deck can go on.
var errRestart = errors.New("restart Windows, then open deck again")

// exists says whether the distribution of deck is installed.
func (w WSL) exists() bool {
	out, err := exec.Command(w.Exe, "-l", "-q").Output()
	if err != nil {
		return false
	}
	for _, name := range strings.Fields(wide(out)) {
		if name == w.Distro {
			return true
		}
	}
	return false
}

// wide is the text of wsl.exe, which writes its own messages in UTF-16.
func wide(b []byte) string {
	if len(b) < 2 || b[1] != 0 {
		return string(b)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return strings.ReplaceAll(string(utf16.Decode(u)), "\x00", "")
}

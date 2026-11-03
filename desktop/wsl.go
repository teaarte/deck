package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// wsl is a command of wsl.exe without a console window, its own messages in UTF-8.
func (w WSL) wsl(args ...string) *exec.Cmd {
	cmd := exec.Command(w.Exe, args...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	hide(cmd)
	return cmd
}

func (w WSL) run(user string, args ...string) *exec.Cmd {
	all := []string{"-d", w.Distro}
	if user != "" {
		all = append(all, "-u", user)
	}
	return w.wsl(append(append(all, "--cd", "~", "--exec"), args...)...)
}

// Command starts deck through the login shell of the user deck, so it finds programs on the PATH their profile sets.
func (w WSL) Command(addr string) *exec.Cmd {
	return w.run("", "bash", "-lc", `exec "$0" "$@"`, "env", "DECK_LAUNCHER=1", Bin, "-addr", addr)
}

func (w WSL) Pair(ctx context.Context) (string, error) {
	out, err := w.run("", Bin, "pair", "--device", "launcher").CombinedOutput()
	return string(out), err
}

func (w WSL) Version(ctx context.Context) (string, error) {
	out, err := w.run("", Bin, "-version").Output()
	return firstWord(out), err
}

// Stop and Kill reach deck inside the distribution: ending wsl.exe alone may leave it running there.
func (w WSL) Stop(*exec.Cmd) error { return w.run("", "pkill", "-TERM", "-x", "deck").Run() }

func (w WSL) Kill(cmd *exec.Cmd) {
	_ = w.run("", "pkill", "-KILL", "-x", "deck").Run()
	_ = cmd.Process.Kill()
}

// provision readies the distribution for deck: bubblewrap for its sandbox and the user deck it runs as; then the
// deck that runs stops, given its time, and the new one takes its place.
const provision = `set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q bubblewrap ca-certificates curl git python3
id deck >/dev/null 2>&1 || useradd -m -s /bin/bash deck
pkill -TERM -x deck || true
for i in $(seq 60); do pgrep -x deck >/dev/null || break; sleep 1; done
install -m 0755 "$(wslpath -u "$0")" ` + Bin

// conf sets in the wsl.conf at $1 the user deck as the default and leaves the PATH of Windows out, whose npm and
// node would stand for those of Linux; the rest of the file stays.
const conf = `set -e
touch "$1"
set_key() {
  awk -v s="[$1]" -v k="$2" -v v="$3" '
    $0 == s { print; print k "=" v; in_s = 1; done = 1; next }
    /^\[/ { in_s = 0 }
    in_s && $0 ~ "^" k " *=" { next }
    { print }
    END { if (!done) { print ""; print s; print k "=" v } }
  ' "$file" > "$file.new"
  cat "$file.new" > "$file"
  rm "$file.new"
}
file=$1
set_key user default deck
set_key interop appendWindowsPath false`

// Ready says whether the distribution of deck is there with deck in it.
func (w WSL) Ready() bool {
	return w.exists() && w.run("", "test", "-x", Bin).Run() == nil
}

// Setup makes deck ready in WSL: WSL itself, the distribution of deck, its packages and the deck binary bin, a path
// on Windows; say tells each step. It is safe to run again, as an update does.
func (w WSL) Setup(bin string, say func(string)) error {
	if err := w.wsl("--status").Run(); err != nil {
		say(tr("Installing WSL; Windows asks for consent and may need a restart."))
		if out, err := w.wsl("--install", "--no-distribution").CombinedOutput(); err != nil {
			return fmt.Errorf("install WSL: %v: %s", err, out)
		}
		return errRestart
	}
	if !w.exists() {
		// A distribution by its own name needs WSL 2.4.4 or later.
		_ = w.wsl("--update").Run()
		say(tr("Installing Ubuntu for deck as the distribution deck."))
		if out, err := w.wsl("--install", "Ubuntu-24.04", "--name", w.Distro, "--no-launch").CombinedOutput(); err != nil {
			return fmt.Errorf("install the distribution: %v: %s", err, out)
		}
	}
	say(tr("Preparing the distribution: bubblewrap and the user deck."))
	say(tr("Putting deck into the distribution."))
	if out, err := w.run("root", "sh", "-c", provision, bin).CombinedOutput(); err != nil {
		return fmt.Errorf("prepare the distribution: %v: %s", err, out)
	}
	if out, err := w.run("root", "sh", "-c", conf, "/etc/wsl.conf").CombinedOutput(); err != nil {
		return fmt.Errorf("set wsl.conf: %v: %s", err, out)
	}
	_ = w.wsl("--terminate", w.Distro).Run()
	return nil
}

// errRestart says WSL was installed and Windows needs a restart before deck can go on.
var errRestart = errors.New("restart Windows, then open deck again")

// exists says whether the distribution of deck is installed.
func (w WSL) exists() bool {
	out, err := w.wsl("-l", "-q").Output()
	if err != nil {
		return false
	}
	for _, name := range strings.Fields(strings.ReplaceAll(string(out), "\x00", "")) {
		if name == w.Distro {
			return true
		}
	}
	return false
}

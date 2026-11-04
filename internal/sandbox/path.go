package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"deck2/internal/release"
)

// Path is the PATH deck finds programs on and gives the programs it starts: its own, then the login shell's, then
// the folders installers put agent CLIs, uv, python and node in; a service starts deck with a short one.
func Path() string {
	dirs := filepath.SplitList(lookup("PATH"))
	dirs = append(dirs, filepath.SplitList(login())...)
	dirs = append(dirs, known()...)
	var out []string
	for _, d := range dirs {
		if filepath.IsAbs(d) && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// Find is the program name on Path, absolute; a path is checked as it is.
func Find(name string) (string, error) {
	if name == "" {
		return "", errors.New("no program named")
	}
	if strings.ContainsRune(name, filepath.Separator) {
		if runnable(name) {
			return name, nil
		}
		return "", fmt.Errorf("%s is not a program", name)
	}
	for _, d := range filepath.SplitList(Path()) {
		if p := filepath.Join(d, name); runnable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
}

func runnable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// login is the PATH of the owner's login shell, asked once for a shell and a home; empty when it does not answer in
// time.
func login() string {
	key := lookup("SHELL") + "\x00" + lookup("HOME")
	logins.Lock()
	defer logins.Unlock()
	if p, ok := logins.of[key]; ok {
		return p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := group(exec.CommandContext(ctx, shell(), "-lc", `printf '\n`+pathMark+`%s\n' "$PATH"`))
	cmd.Env = append(cut([]string{"HOME"}), "PATH="+lookup("PATH"))
	out, _ := cmd.Output()
	p := ""
	for line := range strings.Lines(string(out)) {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), pathMark); ok {
			p = v
		}
	}
	logins.of[key] = p
	return p
}

var logins = struct {
	sync.Mutex
	of map[string]string
}{of: map[string]string{}}

// pathMark tells the PATH a login shell prints from what its profile prints.
const pathMark = "deck-path="

// shell is the owner's login shell: SHELL, else the shell of their line in /etc/passwd, else sh.
func shell() string {
	if s := lookup("SHELL"); filepath.IsAbs(s) {
		return s
	}
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		uid := strconv.Itoa(os.Getuid())
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if f := strings.Split(sc.Text(), ":"); len(f) == 7 && f[2] == uid && filepath.IsAbs(f[6]) {
				return f[6]
			}
		}
	}
	return "/bin/sh"
}

// known are the folders installers put programs in, which an interactive shell alone may add to PATH: the newest
// node of nvm, the user's own bin folders, Homebrew's.
func known() []string {
	var out []string
	if own, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(own, ".local", "bin"))
		if v := nvm(filepath.Join(own, ".nvm")); v != "" {
			out = append(out, filepath.Join(v, "bin"))
		}
		out = append(out, filepath.Join(own, ".npm-global", "bin"), filepath.Join(own, ".cargo", "bin"))
	}
	return append(out, "/opt/homebrew/bin", "/home/linuxbrew/.linuxbrew/bin", "/usr/local/bin")
}

// nvm is the folder of the node nvm starts by default: the newest version its alias default names, else the newest.
func nvm(dir string) string {
	alias, _ := os.ReadFile(filepath.Join(dir, "alias", "default"))
	want := strings.TrimPrefix(strings.TrimSpace(string(alias)), "v")
	versions := filepath.Join(dir, "versions", "node")
	if v := newest(versions, func(name string) bool {
		n := strings.TrimPrefix(name, "v")
		return n == want || strings.HasPrefix(n, want+".")
	}); want != "" && v != "" {
		return v
	}
	return newest(versions, func(string) bool { return true })
}

// newest is the folder of the newest version under dir, named as v22.11.0, of those ok takes; empty when there is
// none.
func newest(dir string, ok func(name string) bool) string {
	entries, _ := os.ReadDir(dir)
	best, at := []int(nil), ""
	for _, e := range entries {
		if v := release.Numbers(e.Name()); e.IsDir() && v != nil && ok(e.Name()) && slices.Compare(v, best) > 0 {
			best, at = v, filepath.Join(dir, e.Name())
		}
	}
	return at
}

// WSL says deck runs in a distribution of WSL, which the owner opens from Windows with wsl -d.
func WSL() bool {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}

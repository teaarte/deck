//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"deck2/internal/home"
)

// bridgeAddr is where a boxed program reaches its door inside its own network namespace.
const bridgeAddr = "127.0.0.1:7777"

// etc are the files of /etc a box sees: users, names, certificates, libraries, fonts, time.
var etc = []string{"ssl", "pki", "ca-certificates", "ca-certificates.conf", "crypto-policies", "passwd", "group",
	"nsswitch.conf", "hosts", "host.conf", "gai.conf", "alternatives", "ld.so.cache", "ld.so.conf", "ld.so.conf.d",
	"fonts", "localtime", "timezone", "mime.types", "os-release", "debian_version", "chromium", "chromium.d",
	"ssh/ssh_config", "gitconfig"}

// system are the folders of programs a box reads whole; /usr holds the rest.
var system = []string{"/opt", "/nix"}

// foreign are the folders of other machines and disks no program on PATH brings into a box: the drives of Windows
// under WSL, the home and host services of a Mac a container machine mounts.
var foreign = []string{"/Users", "/Volumes", "/mnt", "/media", "/var/host-services"}

// builtIn are the folders every box has of its own; no link is made in them.
var builtIn = []string{"/usr", "/etc", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/opt", "/nix", "/proc", "/dev"}

// Fix is what the administrator of the machine does once for bwrap to build boxes in deck's container.
const Fix = "deck's compose.yaml gives its container three lines, security_opt: seccomp=./seccomp.json, " +
	"systempaths=unconfined, apparmor=deck-box; on Ubuntu and Debian the host loads the profile once: " +
	"sudo install -m644 deck-box.apparmor /etc/apparmor.d/deck-box && sudo apparmor_parser -r /etc/apparmor.d/deck-box"

func outside(Box) bool { return false }

func (s *Sandbox) Name() string { return "bubblewrap" }

func (s *Sandbox) program() string {
	if s.Program != "" {
		return s.Program
	}
	if p, err := Find("bwrap"); err == nil {
		return p
	}
	return "bwrap"
}

// check builds an empty box as every box is built; what bwrap says tells which line of the host is missing.
func (s *Sandbox) check() error {
	prog := s.program()
	if !runnable(prog) {
		if _, err := exec.LookPath(prog); err != nil {
			return errors.New("bubblewrap is not installed; install the package bubblewrap (apt install bubblewrap)")
		}
	}
	args := append(append([]string{"--die-with-parent", "--new-session", "--unshare-all"}, roots()...), "--proc", "/proc", "--dev", "/dev", "--", "true")
	out, err := exec.Command(prog, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	said := strings.TrimSpace(string(out) + " " + err.Error())
	why := ""
	switch {
	case strings.Contains(said, "Can't mount proc"):
		why = "systempaths=unconfined is missing (in Apple's container: --masked-path NONE --read-only-path NONE); "
	case strings.Contains(said, "No permissions to create new namespace"), strings.Contains(said, "unshare"):
		why = "the seccomp profile refuses namespaces (seccomp=./seccomp.json is missing); "
	case strings.Contains(said, "RTM_NEWADDR"), strings.Contains(said, "Failed to make / slave"), strings.Contains(said, "Creating new namespace failed"):
		why = "AppArmor refuses namespaces (the profile deck-box is not in force); "
	}
	return fmt.Errorf("bwrap does not build a box here: %s; %s%s", said, why, Fix)
}

// listen opens the socket of a door in dir, or in a folder of the temp one when dir is too long for a socket, which
// it gives to remove with the door.
func (s *Sandbox) listen(dir string) (net.Listener, string, error) {
	if len(filepath.Join(dir, "s")) > 107 {
		short, err := os.MkdirTemp("", "deck-")
		if err != nil {
			return nil, "", err
		}
		ln, err := net.Listen("unix", filepath.Join(short, "s"))
		return ln, short, err
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	return ln, "", err
}

func (s *Sandbox) addr(net.Listener) string { return bridgeAddr }

func (s *Sandbox) command(ctx context.Context, b Box, door *Door, env []string) (*exec.Cmd, error) {
	env = merged(append(base(b), env...))
	args, argv, pinned, err := allowed(b, door, env)
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		closeAll(pinned)
		return nil, err
	}
	var all []byte
	for _, a := range args {
		all = append(append(all, a...), 0)
	}
	go func() { _, _ = w.Write(all); _ = w.Close() }()
	// The options, the environment among them, go through a pipe, so no secret of the box is in the command line the
	// container lists; bwrap takes the program only on it.
	cmd := exec.CommandContext(ctx, s.program(), append([]string{"--args", "3", "--"}, argv...)...)
	cmd.Args[0] = "bwrap"
	if b.Mark != "" {
		cmd.Args[0] = b.Mark
	}
	cmd.Dir, cmd.Env, cmd.ExtraFiles = "/", []string{}, append([]*os.File{r}, pinned...)
	return group(cmd), nil
}

// roots are the system of a box: /usr read-only and the links or folders of the old layout beside it.
func roots() []string {
	args := []string{"--ro-bind", "/usr", "/usr"}
	for _, l := range []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"} {
		fi, err := os.Lstat(l)
		switch {
		case err != nil:
		case fi.Mode()&os.ModeSymlink != 0:
			if to, err := os.Readlink(l); err == nil {
				args = append(args, "--symlink", to, l)
			}
		case fi.IsDir():
			args = append(args, "--ro-bind", l, l)
		}
	}
	return args
}

// allowed are the arguments of bwrap for the box: only what is listed is there; the folders it reads and writes are
// mounted by the files pinned opens, which bwrap takes from fd 4 on and closes.
func allowed(b Box, door *Door, env []string) (args, argv []string, pinned []*os.File, err error) {
	args = append([]string{"--die-with-parent", "--new-session", "--unshare-all"}, roots()...)
	if b.HostNet {
		args = append(args, "--share-net")
	}
	for _, e := range etc {
		args = append(args, "--ro-bind-try", "/etc/"+e, "/etc/"+e)
	}
	for _, d := range system {
		args = append(args, "--ro-bind-try", d, d)
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--tmpfs", "/run", "--tmpfs", "/var/tmp")
	if b.GPU {
		args = append(args, "--dev-bind-try", "/dev/dri", "/dev/dri")
	}
	type mount struct{ flag, from, path, at string }
	var mounts []mount
	add := func(flag, p string) {
		if filepath.IsAbs(p) {
			mounts = append(mounts, mount{flag, real(p), p, real(p)})
		}
	}
	fail := func(err error) ([]string, []string, []*os.File, error) {
		closeAll(pinned)
		return nil, nil, nil, err
	}
	// hold mounts a folder of the box by what pin opens; one that is not there is not in the box.
	hold := func(flag, p string) error {
		if !filepath.IsAbs(p) {
			return nil
		}
		f, at, err := pin(p)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		if err != nil {
			return err
		}
		pinned = append(pinned, f)
		mounts = append(mounts, mount{flag, fmt.Sprint(3 + len(pinned)), p, at})
		return nil
	}
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, r := range programs(path) {
		if !slices.ContainsFunc(foreign, func(f string) bool { return home.Within(f, r) || home.Within(f, real(r)) }) {
			add("--ro-bind-try", r)
		}
	}
	add("--ro-bind-try", b.Argv[0])
	for _, r := range b.Read {
		if err := hold("--ro-bind-fd", r); err != nil {
			return fail(err)
		}
	}
	for _, w := range b.Write {
		if err := hold("--bind-fd", w); err != nil {
			return fail(err)
		}
	}
	for at, p := range b.Shared {
		// What is shared is deck's own and never a link a program put there.
		if fi, err := os.Lstat(p); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) && filepath.IsAbs(at) {
			mounts = append(mounts, mount{"--bind", real(p), at, at})
		}
	}
	// The links along the paths come first, then the paths, shallow before deep, so a path inside another keeps its
	// own mode.
	made := map[string]bool{}
	for _, m := range mounts {
		args = append(args, links(m.path, made)...)
	}
	slices.SortStableFunc(mounts, func(a, b mount) int {
		return strings.Count(a.at, "/") - strings.Count(b.at, "/")
	})
	for _, m := range mounts {
		args = append(args, m.flag, m.from, m.at)
	}
	var private []string
	for _, p := range Private() {
		if slices.ContainsFunc(b.Read, func(r string) bool { return home.Inside(real(r), real(p)) }) {
			private = append(private, p)
		}
	}
	args = append(args, closed(private)...)
	argv = b.Argv
	if door != nil && !b.HostNet {
		self, err := os.Executable()
		if err != nil {
			return fail(err)
		}
		sock := real(door.ln.Addr().String())
		args = append(args, "--bind", filepath.Dir(sock), filepath.Dir(sock), "--ro-bind", real(self), real(self))
		argv = append([]string{real(self), bridgeArg, sock, bridgeAddr, "--"}, argv...)
	}
	dir := b.Dir
	if dir == "" {
		dir = b.Home
	}
	args = append(args, "--chdir", real(dir), "--clearenv")
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		args = append(args, "--setenv", k, v)
	}
	return args, argv, pinned, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// programs are the folders of PATH outside /usr with the installations they belong to, and the runtimes of the
// owner's home on PATH.
func programs(path string) []string {
	own, _ := os.UserHomeDir()
	var out []string
	if own != "" && real(own) != "/" {
		out = Runtimes(own, path)
	}
	for _, d := range filepath.SplitList(path) {
		r := real(d)
		if !filepath.IsAbs(d) || home.Within("/usr", r) || own != "" && home.Inside(real(own), r) {
			continue
		}
		if _, err := os.Stat(r); err != nil {
			continue
		}
		out = append(out, d)
		if up := filepath.Dir(r); up != "/" && installation(up) {
			out = append(out, up)
		}
	}
	return out
}

// links recreate the links along path outside the system of the box, so a program reaches it by the name it was given.
func links(path string, made map[string]bool) []string {
	var args []string
	p := "/"
	for _, part := range strings.Split(filepath.Clean(path), "/")[1:] {
		p = filepath.Join(p, part)
		if slices.ContainsFunc(builtIn, func(b string) bool { return home.Within(b, p) }) {
			break
		}
		fi, err := os.Lstat(p)
		if err != nil {
			break
		}
		if fi.Mode()&os.ModeSymlink != 0 && !made[p] {
			if to, err := os.Readlink(p); err == nil {
				made[p] = true
				args = append(args, "--symlink", to, p)
			}
		}
	}
	return args
}

// closed are the arguments that empty folders and files that exist.
func closed(paths []string) []string {
	var args []string
	for _, h := range paths {
		switch fi, err := os.Stat(h); {
		case err != nil:
		case fi.IsDir():
			args = append(args, "--tmpfs", real(h))
		default:
			args = append(args, "--ro-bind", "/dev/null", real(h))
		}
	}
	return args
}

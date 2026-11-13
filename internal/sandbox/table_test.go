//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func Test_allowed(t *testing.T) {
	own := t.TempDir()
	t.Setenv("HOME", own)
	t.Setenv("DECK_T8_LEAK", "secret")
	for _, d := range []string{".ssh", "code", "code/.ssh", "data", "work"} {
		if err := os.MkdirAll(filepath.Join(own, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	login := filepath.Join(own, "login.json")
	if err := os.WriteFile(login, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(own, "link")
	if err := os.Symlink(filepath.Join(own, "data"), link); err != nil {
		t.Fatal(err)
	}
	box := func(edit func(*Box)) Box {
		b := Box{Argv: []string{"/usr/bin/env"}, Home: filepath.Join(own, "work"), Env: []string{"A=1"}}
		if edit != nil {
			edit(&b)
		}
		return b
	}
	at := func(p string) string { return real(p) }
	for _, row := range []struct {
		box     Box
		has     [][]string
		hasNot  [][]string
		refused bool
		root    bool
		what    string
	}{
		{box(nil), [][]string{unshare, {"--ro-bind", "/usr", "/usr"}, {"--proc", "/proc"}, {"--tmpfs", "/tmp"}, {"--chdir", at(filepath.Join(own, "work"))}, {"--clearenv"}, {"--setenv", "A", "1"}},
			[][]string{{"--share-net"}, {"--dev-bind-try", "/dev/dri", "/dev/dri"}, {"--setenv", "DECK_T8_LEAK", "secret"}, {"--bind-fd"}, {"--ro-bind-fd"}}, false, false,
			"a bare box: its own namespaces, no network, none of deck's environment, no folder of the machine"},
		{box(func(b *Box) { b.HostNet = true }), [][]string{{"--share-net"}, {"--ro-bind-try", "/etc/resolv.conf", "/etc/resolv.conf"}}, nil, false, false,
			"the network of the machine for deck's browser of shots"},
		{box(func(b *Box) { b.GPU = true }), [][]string{{"--dev-bind-try", "/dev/dri", "/dev/dri"}}, [][]string{{"--share-net"}}, false, false,
			"the graphics card"},
		{box(func(b *Box) { b.Read = []string{filepath.Join(own, "data")} }), [][]string{{"--ro-bind-fd", "4", at(filepath.Join(own, "data"))}}, [][]string{{"--bind-fd"}}, false, false,
			"a folder it reads, by the file deck opened"},
		{box(func(b *Box) { b.Write = []string{filepath.Join(own, "data")} }), [][]string{{"--bind-fd", "4", at(filepath.Join(own, "data"))}}, [][]string{{"--ro-bind-fd"}}, false, false,
			"a folder it writes, by the file deck opened"},
		{box(func(b *Box) {
			b.Read = []string{filepath.Join(own, "data")}
			b.Write = []string{filepath.Join(own, "work")}
		}), [][]string{{"--ro-bind-fd", "4", at(filepath.Join(own, "data"))}, {"--bind-fd", "5", at(filepath.Join(own, "work"))}}, nil, false, false,
			"files opened in order, from fd 4 on"},
		{box(func(b *Box) { b.Read = []string{filepath.Join(own, "none"), "relative"} }), nil, [][]string{{"--ro-bind-fd"}}, false, false,
			"a folder that is not there and a relative path are not in the box"},
		{box(func(b *Box) { b.Read = []string{own} }), [][]string{{"--ro-bind-fd", "4", at(own)}, {"--tmpfs", at(filepath.Join(own, ".ssh"))}}, nil, false, false,
			"a home it reads keeps its private folders empty"},
		{box(func(b *Box) { b.Read = []string{filepath.Join(own, "code")} }), nil, [][]string{{"--tmpfs", at(filepath.Join(own, "code", ".ssh"))}}, false, false,
			"a private name below the home is the box's own folder"},
		{box(func(b *Box) { b.Read = []string{link} }), nil, nil, true, true,
			"a folder through a link a program of deck's user could make is refused"},
		{box(func(b *Box) { b.Shared = map[string]string{filepath.Join(own, "work", ".login.json"): login} }), [][]string{{"--bind", at(login), at(filepath.Join(own, "work", ".login.json"))}}, nil, false, false,
			"a shared file of the machine at its path in the box"},
		{box(func(b *Box) { b.Shared = map[string]string{filepath.Join(own, "work", "l"): link} }), nil, [][]string{{"--bind", at(link)}, {"--bind", link}}, false, false,
			"a shared path that is a link is not in the box"},
		{box(func(b *Box) { b.Shared = map[string]string{"relative": login} }), nil, [][]string{{"--bind", at(login)}}, false, false,
			"a shared file at a relative path is not in the box"},
		{box(func(b *Box) { b.Dir = filepath.Join(own, "data") }), [][]string{{"--chdir", at(filepath.Join(own, "data"))}}, nil, false, false,
			"the folder it starts in"},
	} {
		if row.root && os.Getuid() == 0 {
			continue
		}
		args, argv, pinned, err := allowed(row.box, nil, row.box.Env)
		closeAll(pinned)
		if (err != nil) != row.refused {
			t.Errorf("allowed: %v, want refused %v: %s", err, row.refused, row.what)
			continue
		}
		if row.refused {
			continue
		}
		if !slices.Equal(argv, row.box.Argv) {
			t.Errorf("argv %q, want %q without a door: %s", argv, row.box.Argv, row.what)
		}
		if !slices.Equal(args[:len(unshare)], unshare) {
			t.Errorf("args begin %q, want %q: %s", args[:len(unshare)], unshare, row.what)
		}
		clear := slices.Index(args, "--clearenv")
		for i := clear + 1; i < len(args); i += 3 {
			if args[i] != "--setenv" {
				t.Errorf("after --clearenv only --setenv, got %q: %s", args[i], row.what)
				break
			}
		}
		for _, want := range row.has {
			if !carries(args, want) {
				t.Errorf("args lack %q: %s\n%s", want, row.what, strings.Join(args, " "))
			}
		}
		for _, not := range row.hasNot {
			if carries(args, not) {
				t.Errorf("args hold %q: %s\n%s", not, row.what, strings.Join(args, " "))
			}
		}
	}
}

// carries says args carry part in a row.
func carries(args, part []string) bool {
	for i := range args {
		if i+len(part) <= len(args) && slices.Equal(args[i:i+len(part)], part) {
			return true
		}
	}
	return false
}

func Test_parts(t *testing.T) {
	for _, row := range []struct {
		path string
		want []string
		what string
	}{
		{"/a/b", []string{"a", "b"}, "an absolute path"},
		{"a/b", []string{"a", "b"}, "a relative path"},
		{"//a///b/", []string{"a", "b"}, "doubled and trailing slashes"},
		{"/a/./b", []string{"a", "b"}, "a dot"},
		{"/a/../b", []string{"a", "..", "b"}, "dots stay a name the kernel resolves"},
		{"/../a", []string{"..", "a"}, "dots above the root, which the kernel keeps at the root"},
		{"/", nil, "the root"},
		{"", nil, "nothing"},
	} {
		if got := parts(row.path); !slices.Equal(got, row.want) {
			t.Errorf("parts(%q) = %q, want %q: %s", row.path, got, row.want, row.what)
		}
	}
}

func Test_rootLink(t *testing.T) {
	const sticky = 0o1000
	for _, row := range []struct {
		link, dir, mode uint32
		root            bool
		what            string
	}{
		{0, 0, 0o755, true, "root's link in a folder only root writes"},
		{0, 0, 0o700, true, "root's link in root's private folder"},
		{0, 0, 0o777 | sticky, true, "root's link in a sticky folder such as /tmp"},
		{0, 0, 0o775, false, "root's link in a folder its group writes"},
		{0, 0, 0o757, false, "root's link in a folder others write"},
		{1000, 0, 0o755, false, "a link of a user"},
		{0, 1000, 0o755, false, "root's link in a folder of a user, who may replace it"},
		{1000, 0, 0o777 | sticky, false, "a user's link in a sticky folder"},
	} {
		want := row.root && os.Geteuid() != 0
		if got := rootLink(row.link, row.dir, row.mode); got != want {
			t.Errorf("rootLink(%d, %d, %o) = %v, want %v: %s", row.link, row.dir, row.mode, got, want, row.what)
		}
	}
}

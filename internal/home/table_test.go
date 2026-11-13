package home

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestWithin(t *testing.T) {
	for _, row := range []struct {
		dir, path string
		within    bool
		what      string
	}{
		{"/a", "/a", true, "the folder itself"},
		{"/a", "/a/b", true, "a file in it"},
		{"/a", "/a/b/c", true, "a file deeper"},
		{"/a/", "/a/b", true, "a folder written with a slash"},
		{"/a", "/ab", false, "a sibling that starts the same"},
		{"/a", "/a/../b", false, "a way out by dots"},
		{"/a", "/a/b/../../a/c", true, "dots that come back in"},
		{"/a", "/a/..b", true, "a name that starts with two dots"},
		{"/a", "/a/../a", true, "the folder by a way round"},
		{"/a", "/", false, "the root above it"},
		{"/a/b", "/a", false, "the parent"},
		{"/", "/x", true, "under the root"},
		{"/a", "b", false, "a relative path against an absolute folder"},
		{"a", "a/b", true, "two relative paths"},
		{"a", "../a/b", false, "a relative path that leaves first"},
	} {
		if got := Within(row.dir, row.path); got != row.within {
			t.Errorf("Within(%q, %q) = %v, want %v: %s", row.dir, row.path, got, row.within, row.what)
		}
	}
}

func TestResolve(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	for _, f := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{
		"in.txt":   filepath.Join(root, "a.txt"),
		"away.txt": filepath.Join(out, "secret"),
		"rel.txt":  "../" + filepath.Base(out) + "/secret",
		"insub":    "sub",
		"abssub":   filepath.Join(root, "sub"),
		"outdir":   out,
		"dangling": filepath.Join(root, "none"),
	} {
		if err := os.Symlink(to, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, linked); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(root)
	for _, row := range []struct {
		root, name string
		want       string
		what       string
	}{
		{root, "a.txt", "a.txt", "a regular file"},
		{root, "sub/deep/c.txt", "sub/deep/c.txt", "a file deeper"},
		{root, "/a.txt", "a.txt", "a name written absolute stays under the root"},
		{root, "../a.txt", "a.txt", "dots above the root are cut at the root"},
		{root, "sub/../a.txt", "a.txt", "dots that come back in"},
		{root, "insub/b.txt", "sub/b.txt", "a file through a link to a folder inside, by its real path"},
		{root, "abssub/b.txt", "", "a file through an absolute link, though it points inside"},
		{linked, "a.txt", "a.txt", "a root that is itself a link"},
		{root, "in.txt", "", "a link to a file inside is no regular file"},
		{root, "away.txt", "", "a link to a file outside"},
		{root, "rel.txt", "", "a relative link out"},
		{root, "outdir/secret", "", "a file through a link to a folder outside"},
		{root, "dangling", "", "a link to nothing"},
		{root, "none.txt", "", "a missing file"},
		{root, "sub", "", "a folder"},
		{root, "", "", "the root itself"},
		{root, "pipe", "", "a pipe"},
		{filepath.Join(root, "none"), "a.txt", "", "a root that is not there"},
	} {
		got, err := Resolve(row.root, row.name)
		want := ""
		if row.want != "" {
			want = filepath.Join(real, row.want)
		}
		if got != want || (want == "") != (err != nil) {
			t.Errorf("Resolve(%q, %q) = %q, %v; want %q: %s", row.root, row.name, got, err, want, row.what)
		}
	}
}

// tree makes files and links under a new folder: a name ending in / is a folder, "->" a link to what follows.
func tree(t *testing.T, entries ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, e := range entries {
		name, to, link := strings.Cut(e, " -> ")
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		switch {
		case link:
			err = os.Symlink(to, p)
		case strings.HasSuffix(name, "/"):
			err = os.MkdirAll(p, 0o700)
		default:
			err = os.WriteFile(p, []byte(name), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestUnpack(t *testing.T) {
	out := t.TempDir()
	type entry struct {
		name string
		flag byte
		link string
	}
	for _, row := range []struct {
		have    []string
		entries []entry
		keep    string
		files   []string
		refused bool
		what    string
	}{
		{nil, []entry{{"a.txt", tar.TypeReg, ""}, {"sub/", tar.TypeDir, ""}, {"sub/b.txt", tar.TypeReg, ""}}, "", []string{"a.txt", "sub/b.txt"}, false, "files and folders"},
		{nil, []entry{{"deep/er/c.txt", tar.TypeReg, ""}}, "", []string{"deep/er/c.txt"}, false, "a file whose folders the tar does not name"},
		{nil, []entry{{"../x.txt", tar.TypeReg, ""}}, "", nil, true, "a name out by dots"},
		{nil, []entry{{"a/../../x.txt", tar.TypeReg, ""}}, "", nil, true, "dots that leave deeper in"},
		{nil, []entry{{"/etc/x.txt", tar.TypeReg, ""}}, "", nil, true, "an absolute name"},
		{nil, []entry{{"skip/x.txt", tar.TypeReg, ""}}, "", nil, true, "a name the caller does not take"},
		{nil, []entry{{"a.txt", tar.TypeReg, ""}, {"skip/x.txt", tar.TypeReg, ""}}, "", []string{"a.txt"}, true, "a refused name stops what follows"},
		{[]string{"away -> " + out}, []entry{{"away/x.txt", tar.TypeReg, ""}}, "", nil, true, "a file through a link to a folder outside"},
		{[]string{"sub/", "in -> sub"}, []entry{{"in/x.txt", tar.TypeReg, ""}}, "", nil, true, "a file through a link to a folder inside"},
		{[]string{"f.txt -> " + filepath.Join(out, "f.txt")}, []entry{{"f.txt", tar.TypeReg, ""}}, "", nil, true, "a file over a link"},
		{nil, []entry{{"l", tar.TypeSymlink, out}, {"l/x.txt", tar.TypeReg, ""}}, "", []string{"l/x.txt"}, false, "a link the tar carries makes no link: a file under its name stays inside"},
		{nil, []entry{{"h", tar.TypeLink, "a.txt"}}, "", nil, false, "a hard link the tar carries makes nothing"},
		{[]string{"kept.txt"}, []entry{{"kept.txt", tar.TypeReg, ""}}, "kept.txt", []string{"kept.txt"}, false, "a file keep names stays"},
	} {
		dir := tree(t, row.have...)
		var buf bytes.Buffer
		w := tar.NewWriter(&buf)
		for _, e := range row.entries {
			h := &tar.Header{Name: e.name, Typeflag: e.flag, Linkname: e.link, Mode: 0o644}
			body := ""
			if e.flag == tar.TypeReg {
				body = "new"
				h.Size = int64(len(body))
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(body))
		}
		_ = w.Close()
		take := func(name string) bool { return !strings.HasPrefix(name, "skip/") }
		keep := func(name string) bool { return name == row.keep }
		err := Unpack(dir, &buf, take, keep)
		if (err != nil) != row.refused {
			t.Errorf("Unpack: %v, want refused %v: %s", err, row.refused, row.what)
		}
		var got []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				rel, _ := filepath.Rel(dir, p)
				got = append(got, filepath.ToSlash(rel))
			}
			return nil
		})
		if !slices.Equal(got, row.files) {
			t.Errorf("Unpack wrote %q, want %q: %s", got, row.files, row.what)
		}
		if b, err := os.ReadFile(filepath.Join(dir, row.keep)); row.keep != "" && (err != nil || string(b) != row.keep) {
			t.Errorf("a kept file changed: %q %v: %s", b, err, row.what)
		}
		if es, _ := os.ReadDir(out); len(es) != 0 {
			t.Fatalf("Unpack wrote outside: %v: %s", es, row.what)
		}
	}
}

func TestReadIn(t *testing.T) {
	out := tree(t, "secret.txt")
	dir := tree(t, "a.txt", "sub/b.txt", "big.txt", "away.txt -> "+filepath.Join(out, "secret.txt"), "in.txt -> a.txt", "outdir -> "+out, "sub/")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), make([]byte, 11), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name string
		want string
		what string
	}{
		{"a.txt", "a.txt", "a regular file"},
		{"sub/b.txt", "sub/b.txt", "a file deeper"},
		{"in.txt", "a.txt", "a link that stays inside"},
		{"away.txt", "", "a link to a file outside"},
		{"outdir/secret.txt", "", "a file through a link to a folder outside"},
		{"../" + filepath.Base(out) + "/secret.txt", "", "a name out by dots"},
		{"big.txt", "", "a file past the limit"},
		{"sub", "", "a folder"},
		{"pipe", "", "a pipe, read without waiting for a writer"},
		{"none.txt", "", "a missing file"},
	} {
		got, err := ReadIn(dir, row.name, 10)
		if string(got) != row.want || (row.want == "") != (err != nil) {
			t.Errorf("ReadIn(%q) = %q, %v; want %q: %s", row.name, got, err, row.want, row.what)
		}
	}
}

func TestRemoveIn(t *testing.T) {
	for _, row := range []struct {
		have    []string
		names   []string
		left    []string
		refused bool
		what    string
	}{
		{[]string{"a.txt", "b.txt"}, []string{"a.txt"}, []string{"b.txt"}, false, "a file"},
		{[]string{"sub/a.txt", "b.txt"}, []string{"sub"}, []string{"b.txt"}, false, "a folder whole"},
		{[]string{"a.txt"}, []string{"none.txt"}, []string{"a.txt"}, false, "a missing name"},
		{[]string{"a.txt"}, []string{"../a.txt"}, []string{"a.txt"}, true, "a name out by dots"},
		{[]string{"a.txt"}, []string{"/a.txt"}, []string{"a.txt"}, true, "an absolute name"},
		{[]string{"sub/a.txt", "in -> sub"}, []string{"in/a.txt"}, []string{"sub/a.txt"}, true, "a name through a link inside"},
		{[]string{"sub/a.txt", "in -> sub"}, []string{"in"}, []string{"sub/a.txt"}, true, "a link itself"},
	} {
		out := tree(t, "keep.txt")
		have := row.have
		for i, h := range have {
			have[i] = strings.ReplaceAll(h, "OUT", out)
		}
		dir := tree(t, have...)
		err := RemoveIn(dir, row.names)
		if (err != nil) != row.refused {
			t.Errorf("RemoveIn(%q): %v, want refused %v: %s", row.names, err, row.refused, row.what)
		}
		var left []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				rel, _ := filepath.Rel(dir, p)
				left = append(left, filepath.ToSlash(rel))
			}
			return nil
		})
		if !slices.Equal(left, row.left) {
			t.Errorf("RemoveIn(%q) left %q, want %q: %s", row.names, left, row.left, row.what)
		}
		if _, err := os.Stat(filepath.Join(out, "keep.txt")); err != nil {
			t.Errorf("RemoveIn removed outside: %s", row.what)
		}
	}
	link := tree(t, "keep.txt")
	dir := tree(t, "away -> "+link)
	if err := RemoveIn(dir, []string{"away/keep.txt"}); err == nil {
		t.Error("RemoveIn through a link to a folder outside is refused")
	}
	if _, err := os.Stat(filepath.Join(link, "keep.txt")); err != nil {
		t.Error("RemoveIn removed a file outside through a link")
	}
	if err := RemoveIn(filepath.Join(dir, "none"), []string{"a"}); err != nil {
		t.Errorf("RemoveIn in a missing folder removes nothing: %v", err)
	}
}

func TestFresh(t *testing.T) {
	out := tree(t, "target.txt")
	target := filepath.Join(out, "target.txt")
	for _, row := range []struct {
		have    []string
		refused bool
		what    string
	}{
		{nil, false, "a new file"},
		{[]string{"f"}, false, "in place of a file"},
		{[]string{"f -> " + target}, false, "in place of a link to a file outside, which stays as it was"},
		{[]string{"f -> " + filepath.Join(out, "none")}, false, "in place of a link to nothing, which makes nothing outside"},
		{[]string{"f/", "f/x"}, true, "a folder with files is no place for a file"},
	} {
		dir := tree(t, row.have...)
		p := filepath.Join(dir, "f")
		err := Fresh(p, "new")
		if (err != nil) != row.refused {
			t.Errorf("Fresh: %v, want refused %v: %s", err, row.refused, row.what)
		}
		if !row.refused {
			fi, err := os.Lstat(p)
			b, _ := os.ReadFile(p)
			if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || string(b) != "new" {
				t.Errorf("Fresh left %v %v %q: %s", fi.Mode(), err, b, row.what)
			}
		}
		if b, _ := os.ReadFile(target); string(b) != "target.txt" {
			t.Fatalf("Fresh wrote through a link: %s", row.what)
		}
		if _, err := os.Lstat(filepath.Join(out, "none")); err == nil {
			t.Fatalf("Fresh made a file outside: %s", row.what)
		}
	}
}

package lint

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/lint"
)

func TestRepositoryIsInEnglish(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	vs, err := lint.English("../..", strings.Fields(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestEnglishCatchesRussian(t *testing.T) {
	var files []string
	_ = filepath.WalkDir("testdata/english", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel("testdata/english", p)
			files = append(files, rel)
		}
		return err
	})
	vs, err := lint.EnglishOf("testdata/english", files, map[string]string{"kept.md": "kept"}, []string{"x_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{
		"internal/app/a.go:3: Russian outside ru.json, a translation into ru and lint.Russian",
		"starters/demo/manifest.json:3: Russian outside a translation into ru",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

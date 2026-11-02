package lint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	// callers are the files of the client that call commands themselves: the live read, the screen and its press, the
	// run a press started; every other part reads through them or presses a button of the server.
	callers     = []string{"web/src/lib/live.ts", "web/src/screen/useScreen.ts", "web/src/screen/act.ts", "web/src/ui/actions/pressStore.ts"}
	commandCall = regexp.MustCompile(`import\s*\{[^}]*\b(call|invoke)\b[^}]*\}\s*from\s*'(?:\.\.?/)+(?:lib/)?api'`)
	locales     = regexp.MustCompile(`from\s*'[./]*locales/`)
	keyWords    = regexp.MustCompile(`\bt\(\s*['"][a-z_]+\.[a-z_]+['"]`)
)

// Client finds in the client a command called outside the parts that call, words of its own beside deck's
// dictionary and a key of words that is not the English text.
func Client(root string) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(filepath.Join(root, "web", "src"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".ts") && !strings.HasSuffix(rel, ".tsx") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			switch {
			case commandCall.MatchString(line) && !slices.Contains(callers, rel):
				out = append(out, Violation{rel, i + 1, "a command called outside the live read, the screen and the press: read it live or press a button of the server"})
			case locales.MatchString(line):
				out = append(out, Violation{rel, i + 1, "words of the client's own: deck's are in internal/words"})
			case keyWords.MatchString(line):
				out = append(out, Violation{rel, i + 1, "a key of words that is not its English text"})
			}
		}
		return nil
	})
	return out, err
}

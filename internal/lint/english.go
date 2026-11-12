package lint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Russian are the files that keep Russian, each with why; a folder ends in /. The list only shrinks.
var Russian = map[string]string{
	"install.sh":                                         "a Russian yes to the installer's question",
	"internal/app/documents/search.go":                   "Russian endings and the letter yo of the search of memory",
	"internal/gateway/approve.go":                        "a request named in Russian in a question",
	"internal/store/migrations/00068_memory_trigram.sql": "the Russian yo folded in the index of memory",
	"web/src/settings/Appearance.tsx":                    "the name of the Russian language in Russian",
	"web/dist/":                                          "the build of the client: ru.json and libraries",
	"skills/module-builder/SKILL.md":                     "examples of a module's translations",
	"starters/free-models/custom/grid.html":              "the Russian words of the module's own block",
	"tests/harness/manifests.go":                         "modules with Russian translations",
	"tests/harness/screen.go":                            "a module with Russian translations",
	"tests/lint/testdata/bad/internal/words/ru.json":     "a dictionary of the check of words",
	"tests/lint/testdata/english/":                       "the planted lines of the check of this check",
}

// RussianTests are the scenario tests of the Russian locale, of Russian text and of a cut of letters wider than a
// byte; a file joins only by an edit of this list.
var RussianTests = []string{
	"tests/scenarios/browser/browser_chat_test.go",
	"tests/scenarios/browser/browser_language_test.go",
	"tests/scenarios/browser/browser_memory_test.go",
	"tests/scenarios/browser/browser_requests_test.go",
	"tests/scenarios/browser/browser_shell_test.go",
	"tests/scenarios/browser/browser_widgets_test.go",
	"tests/scenarios/chat/chat_known_test.go",
	"tests/scenarios/chat/chat_lang_test.go",
	"tests/scenarios/chat/chat_master_test.go",
	"tests/scenarios/chat/chat_memory_search_test.go",
	"tests/scenarios/chat/chat_requests_test.go",
	"tests/scenarios/chat/chat_told_test.go",
	"tests/scenarios/chat/chat_topics_test.go",
	"tests/scenarios/chat/chat_waits_test.go",
	"tests/scenarios/modules/modules_blocks_test.go",
	"tests/scenarios/modules/modules_buttons_test.go",
	"tests/scenarios/modules/modules_manifest_test.go",
	"tests/scenarios/modules/modules_ship_test.go",
	"tests/scenarios/modules/modules_side_test.go",
	"tests/scenarios/modules/modules_starters_test.go",
	"tests/scenarios/owner/owner_cockpit_test.go",
	"tests/scenarios/owner/owner_desk_test.go",
	"tests/scenarios/owner/owner_files_test.go",
	"tests/scenarios/owner/owner_language_test.go",
	"tests/scenarios/owner/owner_memory_test.go",
	"tests/scenarios/owner/owner_nodes_words_test.go",
	"tests/scenarios/owner/owner_passkeys_test.go",
	"tests/scenarios/owner/owner_press_test.go",
	"tests/scenarios/owner/owner_settings_test.go",
	"tests/scenarios/owner/owner_tailnet_test.go",
	"tests/scenarios/owner/owner_themes_test.go",
	"tests/scenarios/owner/owner_today_test.go",
	"tests/scenarios/owner/owner_update_test.go",
	"tests/scenarios/owner/owner_voice_test.go",
	"tests/scenarios/providers/providers_acp_test.go",
	"tests/scenarios/providers/providers_assignments_test.go",
	"tests/scenarios/providers/providers_find_test.go",
	"tests/scenarios/providers/providers_model_delete_test.go",
	"tests/scenarios/providers/providers_node_health_test.go",
	"tests/scenarios/providers/providers_selfcheck_test.go",
	"tests/scenarios/providers/providers_signin_test.go",
	"tests/scenarios/runs/runs_confirm_test.go",
}

// cyrillic is a letter of the Cyrillic script.
var cyrillic = regexp.MustCompile(`\p{Cyrillic}`)

// English finds lines with Cyrillic in text files outside internal/words/ru.json, the translations into ru of a JSON
// file and the files of Russian and RussianTests; files are paths under root, as git lists them.
func English(root string, files []string) ([]Violation, error) {
	return EnglishOf(root, files, Russian, RussianTests)
}

// EnglishOf is English by other lists, for the check of the check.
func EnglishOf(root string, files []string, kept map[string]string, tests []string) ([]Violation, error) {
	var out []Violation
	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		if rel == "internal/words/ru.json" || slices.Contains(tests, rel) || keptFile(kept, rel) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(src) || bytes.IndexByte(src, 0) >= 0 || !cyrillic.Match(src) {
			continue
		}
		if filepath.Ext(rel) == ".json" {
			out = append(out, russianOutsideTranslations(rel, src)...)
			continue
		}
		for i, line := range strings.Split(string(src), "\n") {
			if cyrillic.MatchString(line) {
				out = append(out, Violation{rel, i + 1, "Russian outside ru.json, a translation into ru and lint.Russian"})
			}
		}
	}
	return out, nil
}

func keptFile(kept map[string]string, rel string) bool {
	for k := range kept {
		if k == rel || strings.HasSuffix(k, "/") && strings.HasPrefix(rel, k) {
			return true
		}
	}
	return false
}

// russianOutsideTranslations are the strings of a JSON file with Cyrillic that no key ru holds, at their lines.
func russianOutsideTranslations(rel string, src []byte) []Violation {
	var v any
	if json.Unmarshal(src, &v) != nil {
		return []Violation{{rel, 1, "Russian in a file that is no JSON"}}
	}
	var out []Violation
	lines := strings.Split(string(src), "\n")
	var walk func(v any, inRu bool)
	walk = func(v any, inRu bool) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if !inRu && cyrillic.MatchString(k) {
					out = append(out, Violation{rel, lineOf(lines, k), fmt.Sprintf("Russian key %q outside a translation into ru", k)})
				}
				walk(x, inRu || k == "ru")
			}
		case []any:
			for _, x := range v {
				walk(x, inRu)
			}
		case string:
			if !inRu && cyrillic.MatchString(v) {
				out = append(out, Violation{rel, lineOf(lines, v), "Russian outside a translation into ru"})
			}
		}
	}
	walk(v, false)
	slices.SortFunc(out, func(a, b Violation) int { return a.Line - b.Line })
	return out
}

// lineOf is the line of the first Cyrillic word of s.
func lineOf(lines []string, s string) int {
	word := regexp.MustCompile(`\p{Cyrillic}+`).FindString(s)
	for i, l := range lines {
		if strings.Contains(l, word) {
			return i + 1
		}
	}
	return 1
}

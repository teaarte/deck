package manifest

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The advice of a warning of a line not in English: of a script, of a prompt.
const (
	ScriptEnglish = "a script writes English and gives the owner's words through the i18n of its manifest"
	PromptEnglish = "a prompt is English; its agent writes in the language deck gives"
)

// mostLines is how many numbers of lines a warning of a language names.
const mostLines = 5

// NotEnglish is the warning of the text of a file of a module, named by where, whose lines hold a word of a script
// other than Latin, such as Cyrillic: their numbers from 1, the first of them cut, and advice; "" when none does.
func NotEnglish(where, text, advice string) string {
	var lines []string
	first, n := "", 0
	for i, line := range strings.Split(text, "\n") {
		if !foreignWord(line) {
			continue
		}
		if n++; n == 1 {
			first = strings.TrimSpace(line)
			if utf8.RuneCountInString(first) > 60 {
				first = string([]rune(first)[:60]) + "…"
			}
		}
		if len(lines) < mostLines {
			lines = append(lines, fmt.Sprint(i+1))
		}
	}
	if n == 0 {
		return ""
	}
	at := "line " + lines[0]
	if n > 1 {
		at = "lines " + strings.Join(lines, ", ")
		if n > len(lines) {
			at += fmt.Sprintf(" and %d more", n-len(lines))
		}
	}
	return fmt.Sprintf("%s: %s not in English (%q): %s", where, at, first, advice)
}

// foreignWord says a line holds two letters in a row of a script other than Latin, so a π alone is not a word.
func foreignWord(line string) bool {
	run := 0
	for _, r := range line {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			if run++; run == 2 {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

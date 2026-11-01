package release

import (
	"regexp"
	"strconv"
	"strings"
)

var semver = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Semver is the first version in what a program says of itself, such as 2.1.281 of "2.1.281 (Claude Code)" or
// 0.165.0 of "codex-cli 0.165.0"; empty when it names none.
func Semver(text string) string { return semver.FindString(text) }

// Numbers are major, minor and patch of the version in text; nil when it names none.
func Numbers(text string) []int {
	v := Semver(text)
	if v == "" {
		return nil
	}
	out := make([]int, 3)
	for i, p := range strings.SplitN(v, ".", 3)[:3] {
		end := strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' })
		if end >= 0 {
			p = p[:end]
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

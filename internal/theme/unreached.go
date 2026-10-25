package theme

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

// unthemed is the ratchet of the files of the client whose values a theme cannot change yet, checked by tests/lint.
//
//go:embed unthemed.txt
var unthemed string

// Unreached names what of the client a theme cannot change yet, by part: how many values in how many files and
// when they go.
func Unreached() []string {
	type part struct{ values, files int }
	var order []string
	parts := map[string]*part{}
	for _, line := range strings.Split(unthemed, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		why := strings.Join(f[2:], " ")
		if parts[why] == nil {
			parts[why] = &part{}
			order = append(order, why)
		}
		parts[why].values += n
		parts[why].files++
	}
	out := []string{}
	for _, why := range order {
		what, when, _ := strings.Cut(why, ": ")
		out = append(out, fmt.Sprintf("%s: %d values in %d files a theme cannot change yet (%s)", what, parts[why].values, parts[why].files, when))
	}
	return out
}

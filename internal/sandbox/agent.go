package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/home"
)

// Agent gives what an agent may not read or write under every path of deck's folder, since macOS links /tmp and
// /var: all of it but the runs and projects of work, then those but its own, and in its own project all but the data.
func Agent(deck home.Layout) func(project, process int64) []string {
	roots := []home.Layout{deck}
	if real, err := filepath.EvalSymlinks(string(deck)); err == nil && real != string(deck) {
		roots = append(roots, home.Layout(real))
	}
	return func(project, process int64) []string {
		var out []string
		for _, r := range roots {
			out = append(out, r.State()+"/**")
			top, _ := os.ReadDir(string(r))
			for _, e := range top {
				switch {
				case e.Name() == "work" || e.Name() == "state":
				case e.IsDir():
					out = append(out, filepath.Join(string(r), e.Name())+"/**")
				default:
					out = append(out, filepath.Join(string(r), e.Name()))
				}
			}
			out = append(out, others(r.Runs(), fmt.Sprint(process))...)
			out = append(out, others(r.Projects(), fmt.Sprint(project))...)
			names := []string{"mcp"}
			entries, _ := os.ReadDir(r.Project(project))
			for _, e := range entries {
				if e.Name() != "data" && !slices.Contains(names, e.Name()) {
					names = append(names, e.Name())
				}
			}
			for _, n := range names {
				out = append(out, filepath.Join(r.Project(project), n)+"/**")
			}
		}
		return out
	}
}

// others are patterns for every folder of dir with a decimal name other than own.
func others(dir, own string) []string {
	var out []string
	for _, n := range Others("", own, "") {
		out = append(out, filepath.Join(dir, n)+"/**")
	}
	return out
}

// Others are name patterns for every name of prefix, a decimal number and suffix but the one with own: other
// lengths of the number, then each other digit at each place.
func Others(prefix, own, suffix string) []string {
	out := []string{prefix + strings.Repeat("?", len(own)+1) + "*" + suffix}
	for n := 1; n < len(own); n++ {
		out = append(out, prefix+strings.Repeat("?", n)+suffix)
	}
	for i := range own {
		for c := '0'; c <= '9'; c++ {
			if byte(c) != own[i] {
				out = append(out, prefix+own[:i]+string(c)+strings.Repeat("?", len(own)-i-1)+suffix)
			}
		}
	}
	return out
}

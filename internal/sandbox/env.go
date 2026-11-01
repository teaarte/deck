package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// kept are the variables of deck every program it starts keeps, with LC_*; HOME, TMPDIR and XDG_* would lead a
// confined one to the owner's settings, and keys never pass.
var kept = []string{"USER", "LOGNAME", "SHELL", "LANG", "TZ", "LC_"}

// environ is deck's own environment.
func environ() []string { return os.Environ() }

// lookup is a variable of deck's environment; empty when it has none.
func lookup(name string) string {
	for _, kv := range slices.Backward(environ()) {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// Kept is deck's environment cut to the kept variables and more, its PATH Path; a name ending in _ keeps every
// variable it begins.
func Kept(more ...string) []string { return append(cut(more), "PATH="+Path()) }

func cut(more []string) []string {
	names := append(slices.Clone(kept), more...)
	return slices.DeleteFunc(environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return !slices.ContainsFunc(names, func(n string) bool { return n == name || strings.HasSuffix(n, "_") && strings.HasPrefix(name, n) })
	})
}

// Display is the environment and the sockets a box with a window takes of the owner's screen: X11 or Wayland.
func Display() (env, read []string) {
	env = cut([]string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR"})
	if lookup("DISPLAY") != "" {
		read = append(read, "/tmp/.X11-unix")
	}
	if x := lookup("XAUTHORITY"); x != "" {
		read = append(read, x)
	}
	if run, w := lookup("XDG_RUNTIME_DIR"), lookup("WAYLAND_DISPLAY"); run != "" && w != "" {
		read = append(read, filepath.Join(run, w))
	}
	return env, read
}

// Proxy is the environment that sends the web of a confined program through deck's proxy, self reached directly.
func Proxy(proxy, self string) []string {
	return []string{"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=" + self, "no_proxy=" + self, "NODE_USE_ENV_PROXY=1"}
}

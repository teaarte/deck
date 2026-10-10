package sandbox

import (
	"os"
	"slices"
	"strings"
)

// kept are the variables of deck every program it starts keeps, with LC_*; HOME, TMPDIR and XDG_* would lead a
// confined one to the owner's settings, and keys never pass.
var kept = []string{"PATH", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "LC_"}

// Kept is deck's environment cut to the kept variables and more; a name ending in _ keeps every variable it begins.
func Kept(more ...string) []string {
	names := append(slices.Clone(kept), more...)
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return !slices.ContainsFunc(names, func(n string) bool { return n == name || strings.HasSuffix(n, "_") && strings.HasPrefix(name, n) })
	})
}

// Env is the environment of a program deck confines: the kept variables, its home and temp folder; a door adds its
// own.
func Env(home, tmp string) []string { return append(Kept(), "HOME="+home, "TMPDIR="+tmp) }

// Proxy is the environment that sends the web of a confined program through deck's proxy, self reached directly.
func Proxy(proxy, self string) []string {
	return []string{"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=" + self, "no_proxy=" + self, "NODE_USE_ENV_PROXY=1"}
}

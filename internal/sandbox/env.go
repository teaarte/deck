package sandbox

import (
	"os"
	"slices"
	"strings"
)

// kept are the variables of deck a confined program keeps, with LC_*; HOME, TMPDIR and XDG_* would lead it to the
// owner's settings, and keys never pass.
var kept = []string{"PATH", "USER", "LOGNAME", "SHELL", "LANG", "TZ"}

// Env is the environment of a program deck confines: the kept variables, its home and temp folder, and with proxy,
// deck's door at self with its password, the proxy of every client, Node's fetch too, with only self past it.
func Env(home, tmp, proxy, self string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return !slices.Contains(kept, name) && !strings.HasPrefix(name, "LC_")
	})
	env = append(env, "HOME="+home, "TMPDIR="+tmp)
	if proxy == "" {
		return env
	}
	return append(env, Proxy(proxy, self)...)
}

// Proxy is the environment that sends the web of a confined program through deck's proxy, self reached directly.
func Proxy(proxy, self string) []string {
	return []string{"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=" + self, "no_proxy=" + self, "NODE_USE_ENV_PROXY=1"}
}

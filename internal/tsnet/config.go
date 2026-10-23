// Package tsnet puts deck on the owner's tailnet from inside the binary: the phone reaches it over HTTPS. Only a
// build with the tag tailnet has it.
package tsnet

// Config is the node deck runs as.
type Config struct {
	Hostname string
	Dir      string
	// ControlURL is the coordination server; empty is Tailscale's.
	ControlURL string
	// Plain serves HTTP on 80 and 8080 instead of HTTPS on 443 and 8443, for a control server without certificates.
	Plain bool
	// Health reports the node: needs_login with the sign-in link, ok with its https address, or error.
	Health func(status, text, address string)
}

// Ports are the port of the client on the node and of its second origin; the caller pairs them before deck serves.
func Ports(plain bool) (main, second int) {
	if plain {
		return 80, 8080
	}
	return 443, 8443
}

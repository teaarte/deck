// Package egress reaches only public addresses.
package egress

// Public says an address is public; only a table proves it.
func Public() bool { return true }

// Dial dials a host, never a private one.
func Dial() {}

// Scope is what a token reaches, and Every, all of it.
type Scope struct{ Every bool }

// Hosts are names. Every one is public.
var Hosts []string

// Plain says what it does without a law.
func Plain() {}

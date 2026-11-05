package node

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/home"
)

// Allowed is what a node lets deck use on its machine: ports of its loopback and agents. Its owner keeps the list in
// the node's file; deck never sends it, so a deck broken into reaches nothing more.
type Allowed struct {
	Ports  []int
	Agents []string
}

var agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParseAllowed reads a list of ports and agents apart by commas, spaces or lines; # begins a comment.
func ParseAllowed(text string) (Allowed, error) {
	var a Allowed
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		for _, w := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			if p, err := strconv.Atoi(w); err == nil {
				if p < 1 || p > 65535 {
					return Allowed{}, fmt.Errorf("%d is no port", p)
				}
				if !slices.Contains(a.Ports, p) {
					a.Ports = append(a.Ports, p)
				}
				continue
			}
			if !agentName.MatchString(w) {
				return Allowed{}, fmt.Errorf("%q is neither a port nor an agent such as claude-code", w)
			}
			if !slices.Contains(a.Agents, w) {
				a.Agents = append(a.Agents, w)
			}
		}
	}
	return a, nil
}

// ReadAllowed is what the node of h allows; nothing when its file is missing or broken.
func ReadAllowed(h home.Layout) Allowed {
	b, err := os.ReadFile(h.NodeAllowed())
	if err != nil {
		return Allowed{}
	}
	a, _ := ParseAllowed(string(b))
	return a
}

// SaveAllowed keeps the list of the node of h in its file.
func SaveAllowed(h home.Layout, a Allowed) error {
	return home.WriteFile(h.NodeAllowed(), []byte("# What deck may use on this machine: ports of its loopback and agents.\n"+a.String()+"\n"))
}

func (a Allowed) String() string {
	var out []string
	for _, p := range a.Ports {
		out = append(out, strconv.Itoa(p))
	}
	return strings.Join(append(out, a.Agents...), ",")
}

// Port says the node lets deck call port of its loopback.
func (a Allowed) Port(port int) bool { return slices.Contains(a.Ports, port) }

// Agent says the node lets deck run turns of agent.
func (a Allowed) Agent(name string) bool { return slices.Contains(a.Agents, name) }

// portOf is the port of a URL, that of its scheme when it names none.
func portOf(scheme, port string) (int, error) {
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0, errors.New("no port")
	}
	return p, nil
}

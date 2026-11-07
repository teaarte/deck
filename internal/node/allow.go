package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/home"
)

// Allowed is what a node lets deck use on its machine: ports of its loopback, agents and folders. Its owner keeps the
// list in the node's file; deck never sends it, so a deck broken into reaches nothing more.
type Allowed struct {
	Ports  []int
	Agents []string
	// Paths are the folders deck's turns and scripts may work in, each with what is inside it; ~ is the user's home.
	Paths []string
}

var agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParseAllowed reads a list of ports, agents and folders apart by commas, spaces or lines; a folder begins with / or ~
// and keeps its spaces up to the next comma; # begins a comment.
func ParseAllowed(text string) (Allowed, error) {
	var a Allowed
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		var words []string
		for _, item := range strings.Split(line, ",") {
			if item = strings.TrimSpace(item); isPath(item) {
				words = append(words, item)
				continue
			}
			words = append(words, strings.Fields(item)...)
		}
		for _, w := range words {
			if isPath(w) {
				p, err := cleanPath(w)
				if err != nil {
					return Allowed{}, err
				}
				if !slices.Contains(a.Paths, p) {
					a.Paths = append(a.Paths, p)
				}
				continue
			}
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
				return Allowed{}, fmt.Errorf("%q is neither a port, an agent such as claude-code, nor a folder such as ~/code/repo", w)
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
	return home.WriteFile(h.NodeAllowed(), []byte("# What deck may use on this machine: ports of its loopback, agents and folders.\n"+a.String()+"\n"))
}

func (a Allowed) String() string {
	var out []string
	for _, p := range a.Ports {
		out = append(out, strconv.Itoa(p))
	}
	return strings.Join(slices.Concat(out, a.Agents, a.Paths), ",")
}

// Empty says the node allows deck nothing.
func (a Allowed) Empty() bool { return len(a.Ports)+len(a.Agents)+len(a.Paths) == 0 }

func isPath(w string) bool {
	return strings.HasPrefix(w, "/") || w == "~" || strings.HasPrefix(w, "~/")
}

// cleanPath is a folder of the list as its owner wrote it, cleaned.
func cleanPath(w string) (string, error) {
	rest, home := strings.CutPrefix(w, "~")
	if home {
		if rest == "" {
			return "~", nil
		}
		return "~" + filepath.Clean(rest), nil
	}
	if !filepath.IsAbs(w) {
		return "", fmt.Errorf("%q is no absolute folder", w)
	}
	return filepath.Clean(w), nil
}

// expand is a folder of the list on this machine, ~ the user's home.
func expand(p string) (string, error) {
	rest, ok := strings.CutPrefix(p, "~")
	if !ok {
		return p, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, rest), nil
}

// NotAllowed is a folder the owner of a node did not allow deck, with what adds it.
type NotAllowed struct{ Path string }

func (e *NotAllowed) Error() string {
	return fmt.Sprintf("folder %s is not allowed on this node: on its machine add it with deck -join <deck's address> --install --allow %s", e.Path, e.Path)
}

// Folder is the real path of a folder of this machine its owner allows deck: one of Paths or inside one that is not,
// and is not inside, any of own, the folders of the node and of a deck on its machine.
func (a Allowed) Folder(path string, own []string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is no absolute folder", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("folder %s is not on this machine", path)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s on this machine is not a folder", path)
	}
	for _, o := range own {
		for _, n := range home.Names(o) {
			if home.Overlap(n, real) {
				return "", fmt.Errorf("folder %s holds or is inside %s, a folder of deck on this machine", path, o)
			}
		}
	}
	for _, p := range a.Paths {
		at, err := expand(p)
		if err != nil {
			continue
		}
		if r, err := filepath.EvalSymlinks(at); err == nil && home.Within(r, real) {
			return real, nil
		}
	}
	return "", &NotAllowed{Path: path}
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

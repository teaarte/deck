package harness

import (
	"context"
	"net/url"
	"strconv"
	"sync"

	"deck2/internal/node"
)

// Node is a node of a test deck on this machine: paired by a code of pair_link, it runs until Stop or the end of the
// test and serves deck the ports of its loopback and the agents its owner allows.
type Node struct {
	e      *Env
	Joined node.Joined
	// Serving is what the node runs; its Allowed is what Allow set.
	Serving node.Serving
	mu      sync.Mutex
	allowed node.Allowed
	stop    context.CancelFunc
	done    chan struct{}
}

// Node pairs a node named name with deck and starts it; allow are what its owner allows: the URLs of services of its
// machine, by their ports, and agents.
func (e *Env) Node(name string, allow ...string) *Node {
	e.T.Helper()
	n := e.PairNode(name)
	n.Allow(allow...)
	n.Start()
	return n
}

// PairNode pairs a node named name with deck without starting it.
func (e *Env) PairNode(name string) *Node {
	e.T.Helper()
	var link struct {
		Code string
		Join []string
	}
	e.Must("deck.pair_link", map[string]any{"kind": "node", "name": name}, &link)
	j, err := node.Pair(context.Background(), e.URL, link.Code, "")
	if err != nil {
		e.T.Fatal(err)
	}
	n := &Node{e: e, Joined: j}
	n.Serving.Allowed = func() node.Allowed {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.allowed
	}
	e.T.Cleanup(n.Stop)
	return n
}

// Allow sets what the owner of the node allows, as an edit of its file: URLs by their ports, and agents.
func (n *Node) Allow(allow ...string) {
	var a node.Allowed
	for _, s := range allow {
		if u, err := url.Parse(s); err == nil && u.Port() != "" {
			p, _ := strconv.Atoi(u.Port())
			a.Ports = append(a.Ports, p)
		} else {
			a.Agents = append(a.Agents, s)
		}
	}
	n.mu.Lock()
	n.allowed = a
	n.mu.Unlock()
}

// Start runs the node, connecting to deck again and again while it is not there.
func (n *Node) Start() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.stop, n.done = cancel, make(chan struct{})
	go func() {
		defer close(n.done)
		node.Run(ctx, n.Joined, n.Serving)
	}()
}

// Stop ends the node, as when its machine sleeps, and waits until it is gone.
func (n *Node) Stop() {
	n.mu.Lock()
	stop, done := n.stop, n.done
	n.stop = nil
	n.mu.Unlock()
	if stop != nil {
		stop()
		<-done
	}
}

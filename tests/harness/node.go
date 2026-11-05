package harness

import (
	"context"
	"sync"

	"deck2/internal/node"
)

// Node is a node of a test deck on this machine: paired by a code of pair_link, it runs until Stop or the end of the
// test and serves deck the addresses of its loopback.
type Node struct {
	e      *Env
	Joined node.Joined
	mu     sync.Mutex
	stop   context.CancelFunc
	done   chan struct{}
}

// Node pairs a node named name with deck and starts it.
func (e *Env) Node(name string) *Node {
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
	n.Start()
	e.T.Cleanup(n.Stop)
	return n
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
		node.Run(ctx, n.Joined)
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

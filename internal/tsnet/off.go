//go:build !tailnet

package tsnet

import (
	"context"
	"errors"

	"deck2/internal/api"
)

// Built says this deck joins a tailnet: a build without the tag tailnet does not.
const Built = false

// Node is deck on the tailnet, which this build has not.
type Node struct{}

// Start refuses: this build has no tailnet.
func Start(context.Context, Config, *api.Server) (*Node, error) {
	return nil, errors.New("deck is built without tailnet; build it with go build -tags tailnet")
}

func (n *Node) Close() error { return nil }

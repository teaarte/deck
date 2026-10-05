// Package chat is the chat of the owner with the master: topics, messages, the reply turns of the master and the
// memory its agents get.
package chat

import (
	"context"
	"deck2/internal/events"
	"deck2/internal/provider"
	"fmt"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
)

const (
	roleOwner  = "owner"
	roleMaster = "master"
	// FileOwner is the owner kind of a file attached to a message.
	FileOwner = "chat_message"
	// retellTail is how many messages after the summary a new session gets word for word.
	retellTail = 30
	// summaryDue is how many messages since the summary make deck ask the master to update it.
	summaryDue = 20
)

// Service keeps the chat. For is the provider a command runs on; Runner starts and goes on with the replies.
type Service struct {
	St     *store.Store
	Bus    *events.Bus
	Now    func() time.Time
	Asks   *ask.Broker
	Runner *process.Runner
	For    func(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// RemoveFiles deletes the files whose rows are gone.
	RemoveFiles func(ids []int64)

	mu    sync.Mutex
	queue chatQueue
	// unstarted is why the reply of a topic did not start, until one does.
	unstarted map[int64]string
	// start keeps a reply starting apart from a topic being deleted.
	start sync.Mutex
}

// NewHook is the hook of the runner that keeps the master's replies in their topics.
func (ch *Service) NewHook() Hook { return Hook{ch: ch} }

// summaryName is the document of project master with the summary of a topic.
func summaryName(topic int64) string { return fmt.Sprintf("topics/%d/summary.md", topic) }

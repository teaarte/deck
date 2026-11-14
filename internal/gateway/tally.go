package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Reached is the outcome of a line of the journal that counts the requests the programs of a box sent to a host.
const Reached = "reached"

// The bounds of a tally: the hosts it names apart, the rest under "*"; and when it writes before its end.
const (
	tallyHosts = 50
	tallyBatch = 100
	tallyEvery = 10 * time.Minute
)

// tally counts the requests the programs of a box sent through its door, by host, and writes them to the journal
// under the right network of whom: at its end, and on the way once a host has a batch or a while passed.
type tally struct {
	g       *Gateway
	act     registry.Actor
	project int64
	who     string

	mu      sync.Mutex
	hosts   map[string]int64
	written time.Time
}

func (g *Gateway) tallyOf(act registry.Actor, project int64, who string) *tally {
	return &tally{g: g, act: act, project: project, who: who, hosts: map[string]int64{}, written: g.cfg.Now()}
}

// Tally hears the hosts the box of a module's program reaches, for a box with no session, such as an MCP server: it
// writes them on the way, by batch or after a while.
func (g *Gateway) Tally(workspace, project int64) func(host string) {
	return g.tallyOf(registry.Actor{Kind: registry.Agent, Workspace: workspace, ProjectID: project}, project, manifest.Programs).reach
}

func (t *tally) reach(host string) {
	if t == nil {
		return
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return
	}
	t.mu.Lock()
	if _, ok := t.hosts[host]; !ok && len(t.hosts) >= tallyHosts {
		host = "*"
	}
	t.hosts[host]++
	due := t.hosts[host] >= tallyBatch || t.g.cfg.Now().Sub(t.written) >= tallyEvery
	t.mu.Unlock()
	if due {
		t.flush()
	}
}

// flush writes the hosts the tally holds to the journal, a line a host with its count, and starts it again.
func (t *tally) flush() {
	if t == nil {
		return
	}
	t.mu.Lock()
	hosts := t.hosts
	t.hosts, t.written = map[string]int64{}, t.g.cfg.Now()
	t.mu.Unlock()
	if len(hosts) == 0 {
		return
	}
	act := t.act
	err := t.g.st.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		for _, host := range slices.Sorted(maps.Keys(hosts)) {
			_, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(t.g.cfg.Now()), WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID),
				ProjectID: store.ID(t.project), Actor: act.Author(), Caller: frozenCaller(act), Tool: manifest.Network, Need: manifest.Network, Subject: host,
				Count: hosts[host], Outcome: Reached, Params: fmt.Sprintf(`{"who": %q}`, t.who)})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("journal the hosts a box reached", "err", err)
	}
}

package owners

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// nodeGrace is how long deck waits after its start for the nodes to connect again before their rows say they are not.
const nodeGrace = 30 * time.Second

// NodeChanged writes the health row of a node that came or went.
func (s *Service) NodeChanged(device int64, _ bool) {
	s.Go(func(ctx context.Context) { s.nodeHealth(ctx, device) })
}

// FollowNodes writes the rows of the nodes again as their connections or names change, and of the nodes revoked.
func (s *Service) FollowNodes(bus *events.Bus) {
	bus.Follow(s.Go, []string{"models", "devices"}, func(ctx context.Context, workspace int64, _ []string) {
		nodes, err := s.St.Q.ListNodes(ctx)
		if err != nil {
			return
		}
		for _, d := range nodes {
			if workspace == 0 || d.WorkspaceID == workspace {
				s.nodeHealth(ctx, d.ID)
			}
		}
	})
}

// Nodes writes the rows of the nodes that did not connect again a while after deck started.
func (s *Service) Nodes(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(nodeGrace):
	}
	nodes, err := s.St.Q.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, d := range nodes {
		if !d.RevokedAt.Valid && !s.Node(d.ID).Online {
			s.nodeHealth(ctx, d.ID)
		}
	}
}

// nodeHealth writes the row of a node: online or not since when, the connections it serves and what to do; a revoked
// node has none.
func (s *Service) nodeHealth(ctx context.Context, device int64) {
	d, err := s.St.Q.NodeByID(ctx, device)
	if err != nil {
		return
	}
	of, subject := health.Of{Workspace: d.WorkspaceID}, health.Node(d.Name)
	if d.RevokedAt.Valid {
		if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return health.Clear(ctx, q, of, subject) }); err != nil {
			slog.Error("clear health", "err", err)
		}
		return
	}
	conns, err := s.St.Q.NodeConnections(ctx, store.ID(device))
	if err != nil {
		return
	}
	st := s.Node(device)
	since := st.Since
	if since.IsZero() {
		since = parsed(cmp.Or(d.SeenAt.String, d.CreatedAt))
	}
	at := since.Local().Format("2006-01-02 15:04")
	status, text := health.OK, ""
	switch {
	case st.Online && len(conns) == 0:
		text = fmt.Sprintf("node %s is online since %s; no connection goes through it yet: set it as the node of a connection in Settings → Models → Connections", d.Name, at)
	case st.Online:
		addrs := make([]string, 0, len(conns))
		for _, c := range conns {
			addrs = append(addrs, hostPort(c.Url))
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		up, err := s.Probe(pctx, device, addrs)
		cancel()
		var serves, silent []string
		for _, c := range conns {
			if err == nil && !slices.Contains(up, hostPort(c.Url)) {
				silent = append(silent, c.Name)
			} else {
				serves = append(serves, c.Name)
			}
		}
		text = fmt.Sprintf("node %s is online since %s; it serves %s", d.Name, at, strings.Join(serves, ", "))
		if len(silent) > 0 {
			status = health.Warning
			text = fmt.Sprintf("node %s is online since %s; it serves %s, and %s do not answer on its machine", d.Name, at,
				cmp.Or(strings.Join(serves, ", "), "nothing"), strings.Join(silent, ", "))
		}
	default:
		status = health.Warning
		address := cmp.Or(st.Address, s.deckAddress())
		names := make([]string, 0, len(conns))
		for _, c := range conns {
			names = append(names, c.Name)
		}
		text = fmt.Sprintf("node %s is offline since %s. On its machine run deck -join %s --install", d.Name, at, address)
		if len(names) > 0 {
			text = fmt.Sprintf("node %s is offline since %s; the calls of %s go to the next model of their fallbacks. On its machine run deck -join %s --install",
				d.Name, at, strings.Join(names, ", "), address)
		}
	}
	if err := health.Write(ctx, s.St, of, subject, status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}

// deckAddress is the address of deck a node joins by: the first https one, else 127.0.0.1 on deck's machine.
func (s *Service) deckAddress() string {
	var loop string
	for _, o := range s.Origins() {
		if o.Secure() {
			return o.Main
		}
		if u, err := url.Parse(o.Main); err == nil && u.Hostname() == "127.0.0.1" {
			loop = o.Main
		}
	}
	return cmp.Or(loop, "<deck's address>")
}

// hostPort is the host and port of an address, the port of its scheme when it names none.
func hostPort(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	if u.Port() != "" {
		return u.Host
	}
	return net.JoinHostPort(u.Hostname(), map[string]string{"https": "443"}[u.Scheme]+map[string]string{"http": "80"}[u.Scheme])
}

func parsed(t string) time.Time {
	at, _ := time.Parse(store.TimeLayout, t)
	return at
}

package owners

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
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
	folders, err := s.St.Q.ListNodeFolders(ctx, device)
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
	address := cmp.Or(st.Address, s.deckAddress())
	switch {
	case st.Online && len(conns) == 0 && len(folders) == 0 && len(st.Agents) > 0:
		text = fmt.Sprintf("node %s is online since %s; it runs the agents %s", d.Name, at, strings.Join(st.Agents, ", "))
	case st.Online && len(conns) == 0 && len(folders) == 0:
		text = fmt.Sprintf("node %s is online since %s; no connection goes through it yet: set it as the node of a connection in Settings → Models → Connections", d.Name, at)
	case st.Online:
		addrs := make([]string, 0, len(conns))
		for _, c := range conns {
			addrs = append(addrs, hostPort(c.Url))
		}
		paths := make([]string, 0, len(folders))
		for _, f := range folders {
			if !slices.Contains(paths, f.Path) {
				paths = append(paths, f.Path)
			}
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		got, err := s.Probe(pctx, device, addrs, paths)
		cancel()
		var serves, silent, ports []string
		for _, c := range conns {
			switch at := hostPort(c.Url); {
			case err == nil && slices.Contains(got.Denied, at):
				if _, port, _ := net.SplitHostPort(at); !slices.Contains(ports, port) {
					ports = append(ports, port)
				}
			case err == nil && !slices.Contains(got.Up, at):
				silent = append(silent, c.Name)
			default:
				serves = append(serves, c.Name)
			}
		}
		switch {
		case len(conns) == 0 && len(st.Agents) > 0:
			text = fmt.Sprintf("node %s is online since %s; it runs the agents %s", d.Name, at, strings.Join(st.Agents, ", "))
		default:
			text = fmt.Sprintf("node %s is online since %s", d.Name, at)
		}
		switch len(serves) {
		case 0:
		case 1:
			text = fmt.Sprintf("%s; it serves the connection %s", text, names(serves))
		default:
			text = fmt.Sprintf("%s; it serves the connections %s", text, names(serves))
		}
		switch len(silent) {
		case 0:
		case 1:
			status = health.Warning
			text = fmt.Sprintf("%s; the connection %s does not answer on its machine", text, names(silent))
		default:
			status = health.Warning
			text = fmt.Sprintf("%s; the connections %s do not answer on its machine", text, names(silent))
		}
		if len(ports) > 0 {
			status = health.Warning
			text = fmt.Sprintf("node %s is online since %s; port %s is not allowed on it: on its machine add it with deck -join %s --install --allow %s",
				d.Name, at, strings.Join(ports, ", "), address, strings.Join(ports, ","))
		}
		var holds, denied []string
		if err != nil && len(folders) > 0 {
			status = health.Warning
			text = fmt.Sprintf("%s; it did not say what of its folders deck may use: %v", text, err)
			folders = nil
		}
		for _, f := range folders {
			got := got.Folders[f.Path]
			switch {
			case got.Real != "":
				holds = append(holds, f.Project+" "+f.Name)
			case got.Denied:
				status = health.Warning
				if q := "'" + f.Path + "'"; !slices.Contains(denied, q) {
					denied = append(denied, q)
				}
			default:
				status = health.Warning
				text = fmt.Sprintf("%s; folder %s of %s is not on its machine: %s", text, f.Name, f.Project, got.Error)
			}
		}
		if len(denied) > 0 {
			text = fmt.Sprintf("%s; folder %s is not allowed on it: on its machine add it with deck -join %s --install --allow %s",
				text, strings.Join(denied, ", "), address, strings.Join(denied, ","))
		}
		if len(holds) > 0 {
			text = fmt.Sprintf("%s; it holds the folders %s", text, strings.Join(holds, ", "))
		}
	default:
		status = health.Warning
		names := make([]string, 0, len(conns))
		for _, c := range conns {
			names = append(names, c.Name)
		}
		text = fmt.Sprintf("node %s is offline since %s. On its machine run deck -join %s --install", d.Name, at, address)
		if len(names) > 0 {
			text = fmt.Sprintf("node %s is offline since %s; the calls of %s go to the next model of their fallbacks. On its machine run deck -join %s --install",
				d.Name, at, strings.Join(names, ", "), address)
		}
		if len(folders) > 0 {
			text = fmt.Sprintf("%s; the turns and scripts of its folders wait for it", text)
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

// names are names in quotes, which a translation keeps as they are.
func names(ns []string) string {
	q := make([]string, len(ns))
	for i, n := range ns {
		q[i] = strconv.Quote(n)
	}
	return strings.Join(q, ", ")
}

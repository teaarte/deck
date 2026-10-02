// Package tsnet puts deck on the owner's tailnet from inside the binary: the phone reaches it over HTTPS.
package tsnet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"

	"deck2/internal/api"
)

// Config is the node deck runs as.
type Config struct {
	Hostname string
	Dir      string
	// ControlURL is the coordination server; empty is Tailscale's.
	ControlURL string
	// Plain serves HTTP on 80 and 8080 instead of HTTPS on 443 and 8443, for a control server without certificates.
	Plain bool
	// Health reports the node: needs_login with the sign-in link, ok, or error.
	Health func(status, text string)
}

// Node is deck on the tailnet.
type Node struct {
	cfg Config
	ts  *tsnet.Server
}

// Start joins the tailnet in the background and serves the client and its second origin on the node's ports.
func Start(ctx context.Context, cfg Config, srv *api.Server) (*Node, error) {
	ts := &tsnet.Server{Hostname: cfg.Hostname, Dir: cfg.Dir, ControlURL: cfg.ControlURL, Logf: logger.Discard,
		UserLogf: func(format string, args ...any) { slog.Debug("tailscale", "msg", fmt.Sprintf(format, args...)) }}
	if err := ts.Start(); err != nil {
		return nil, err
	}
	n := &Node{cfg: cfg, ts: ts}
	main, second := 443, 8443
	if cfg.Plain {
		main, second = 80, 8080
	}
	srv.Pair(main, second)
	go n.watch(ctx)
	go n.serveAll(ctx, srv, main, second)
	return n, nil
}

// serveAll listens on the node's ports once the owner signed it in; until then deck runs on its local port.
func (n *Node) serveAll(ctx context.Context, srv *api.Server, main, second int) {
	for port, h := range map[int]http.Handler{main: srv.Handler(), second: srv.Sandbox()} {
		ln, err := n.listen(port)
		if err != nil {
			n.health("error", err.Error())
			return
		}
		go serve(ctx, ln, h)
	}
	if !n.cfg.Plain {
		ln, err := n.ts.Listen("tcp", ":80")
		if err != nil {
			n.health("error", err.Error())
			return
		}
		go serve(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusMovedPermanently)
		}))
	}
}

func (n *Node) listen(port int) (net.Listener, error) {
	addr := net.JoinHostPort("", strconv.Itoa(port))
	if n.cfg.Plain {
		return n.ts.Listen("tcp", addr)
	}
	return n.ts.ListenTLS("tcp", addr)
}

func serve(ctx context.Context, ln net.Listener, h http.Handler) {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
		slog.Error("tailscale serve", "err", err)
	}
}

// watch reports the sign-in link while the node waits for the owner, then its name once it runs.
func (n *Node) watch(ctx context.Context) {
	lc, err := n.ts.LocalClient()
	if err != nil {
		n.health("error", err.Error())
		return
	}
	last := ""
	for {
		st, err := lc.StatusWithoutPeers(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			n.report(&last, "error", err.Error())
		case st.BackendState == ipn.Running.String():
			name := ""
			if st.Self != nil {
				name = st.Self.DNSName
			}
			n.report(&last, "ok", "https://"+trimDot(name))
		case st.AuthURL != "":
			n.report(&last, "needs_login", "sign in to Tailscale: "+st.AuthURL)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (n *Node) report(last *string, status, text string) {
	if *last == status+text {
		return
	}
	*last = status + text
	if status == "needs_login" {
		slog.Info("tailscale", "login", text)
	}
	n.health(status, text)
}

func (n *Node) health(status, text string) {
	if n.cfg.Health != nil {
		n.cfg.Health(status, text)
	}
}

func trimDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}

// Close leaves the tailnet.
func (n *Node) Close() error { return n.ts.Close() }

// LocalClient talks to the node itself: its state and addresses.
func (n *Node) LocalClient() (*local.Client, error) { return n.ts.LocalClient() }

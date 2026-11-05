package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/auth"
	"deck2/internal/home"
	deckhome "deck2/internal/home"
	"deck2/internal/release"
	"deck2/internal/sandbox"
	"deck2/internal/tsnet"
	"deck2/web"
)

func main() {
	if err := cleanEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "deck: restart with a clean environment:", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && (os.Args[1] == "-join" || os.Args[1] == "--join") {
		if err := join(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "deck:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "pair" {
		if err := pair(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "deck:", err)
			os.Exit(1)
		}
		return
	}
	restart, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
	if restart != nil {
		// deck stopped whole; it starts again in the new binary, or in the old one when that will not start.
		err := syscall.Exec(restart.Exe, os.Args, os.Environ())
		if rerr := os.Rename(restart.Old, restart.Exe); rerr == nil {
			err = syscall.Exec(restart.Exe, os.Args, os.Environ())
		}
		fmt.Fprintln(os.Stderr, "deck: restart after the update:", err)
		os.Exit(1)
	}
}

func run() (*release.Restart, error) {
	addr := flag.String("addr", cmp.Or(os.Getenv("DECK_ADDR"), "127.0.0.1:7700"), "listen address; the next port is the second origin of custom blocks")
	tailnet := flag.String("tailscale", os.Getenv("DECK_TAILSCALE"), "join the owner's tailnet under this name and serve HTTPS there")
	public := flag.String("public", os.Getenv("DECK_PUBLIC"), "the https addresses of proxies deck is reached through, comma-separated, such as https://deck.example.com; port 8443 of each is its second origin")
	proxy := flag.String("proxy", os.Getenv("DECK_PROXY"), "the proxies before deck whose X-Forwarded-For it takes besides loopback, comma-separated addresses, networks or names, such as caddy")
	version := flag.Bool("version", false, "print the version, commit and build of deck and quit")
	flag.Parse()
	if *version {
		fmt.Println(release.Version, release.Commit, release.Variant(tsnet.Built))
		return nil, nil
	}
	if *tailnet != "" && !tsnet.Built {
		return nil, errors.New("deck: -tailscale needs a deck built with go build -tags tailnet")
	}
	var publics []string
	for p := range strings.SplitSeq(*public, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p == "" {
			continue
		}
		if u, err := url.Parse(p); err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" {
			return nil, errors.New("deck: -public is an https address without a path, such as https://deck.example.com")
		}
		publics = append(publics, p)
	}
	// What deck writes is its own.
	syscall.Umask(0o077)

	layout, err := homeDir()
	if err != nil {
		return nil, err
	}
	home := string(layout)
	// A second deck of the home would take the agents of the first for those a crash left, and kill them.
	lock, err := lockHome(layout)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := os.Chmod(deckhome.Layout(home).State(), 0o700); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := pending(deckhome.Layout(home)); err != nil {
		return nil, err
	}
	ln, sideLn, err := listenPair(*addr)
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// deck.localhost is ::1 first on macOS: deck answers there too, and starts not when another server holds it.
	mains, sides := []net.Listener{ln}, []net.Listener{sideLn}
	if a := ln.Addr().(*net.TCPAddr); a.IP.Equal(net.IPv4(127, 0, 0, 1)) || a.IP.Equal(net.IPv4zero) {
		ln6, side6, err := listenPair(net.JoinHostPort("::1", strconv.Itoa(port)))
		switch {
		case err == nil:
			mains, sides = append(mains, ln6), append(sides, side6)
		case errors.Is(err, syscall.EADDRINUSE):
			return nil, fmt.Errorf("another server holds [::1]:%d or [::1]:%d, where browsers open deck.localhost: %w", port, port+1, err)
		case a.IP.IsLoopback():
			slog.Warn("deck does not listen on ::1", "err", err)
		}
	}
	origins := auth.NewOrigins()
	for _, p := range publics {
		u, _ := url.Parse(p)
		origins.Add(p, "https://"+u.Hostname()+":8443")
	}
	if a := ln.Addr().(*net.TCPAddr); a.IP.IsLoopback() || a.IP.IsUnspecified() {
		for _, o := range auth.Here(port) {
			origins.Add(o.Main, o.Side)
		}
	} else {
		// deck's own browser opens its pages at the one address deck listens at; no browser signs in there.
		base := "http://" + opened(a)
		origins.Add(base, "http://"+net.JoinHostPort(a.IP.String(), strconv.Itoa(port+1)))
	}
	if err := origins.Keep(layout.Origins()); err != nil {
		return nil, err
	}
	restarts := make(chan release.Restart, 1)
	a, err := app.New(app.Config{
		Home: home, MCPBase: mcpBase(ln.Addr().(*net.TCPAddr)),
		QuestionWait: 30 * time.Minute, ConfirmWait: 30 * time.Minute, ConfirmTTL: 24 * time.Hour,
		Variant: release.Variant(tsnet.Built), Image: os.Getenv("DECK_IMAGE"), Origins: origins,
		Restart: func(r release.Restart) {
			select {
			case restarts <- r:
			default:
			}
		},
	})
	if err != nil {
		return nil, err
	}
	defer a.Close()

	// An update ends deck as a signal does, the signals still heard meanwhile.
	base, end := context.WithCancel(context.Background())
	defer end()
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()
	var restart atomic.Pointer[release.Restart]
	go func() {
		select {
		case r := <-restarts:
			restart.Store(&r)
			end()
		case <-ctx.Done():
		}
	}()

	// deck's MCP server answers before any turn starts, a resumed one too; the rest waits until deck has started.
	server := api.New(a, web.FS(), strings.Split(*proxy, ","))
	var started atomic.Bool
	handler := server.Handler()
	// No write timeout: the stream of events stays open.
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !started.Load() && !strings.HasPrefix(r.URL.Path, "/mcp/") {
			http.Error(w, "deck is starting", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	side := &http.Server{Handler: server.Sandbox(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute,
		WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	served := make(chan error, len(mains)+len(sides))
	for _, l := range sides {
		go func() { served <- side.Serve(l) }()
	}
	for _, l := range mains {
		go func() { served <- srv.Serve(l) }()
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		a.Stop()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = side.Shutdown(sctx)
		_ = srv.Shutdown(sctx)
	}()

	a.Run(ctx, 30*time.Second)
	started.Store(true)

	if *tailnet != "" {
		node, err := tsnet.Start(ctx, tsnet.Config{Hostname: *tailnet, Dir: a.Home.Tailscale(), Health: a.TailnetHealth}, server)
		if err != nil {
			return nil, fmt.Errorf("tailscale: %w", err)
		}
		defer node.Close()
	}
	// The update this deck is has served, so the binary before it goes.
	if p, ok := release.ReadPending(a.Home.Update()); ok {
		os.Remove(p.Old)
		os.Remove(a.Home.Update())
	}
	slog.Info("deck listening", "url", url0(origins, ln), "public", strings.Join(publics, ","), "home", home)
	// The first device pairs by a link in the log; deck pair makes the next ones.
	links, err := a.Owners.Welcome(ctx, a.Auth.Signed(ctx))
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		slog.Info("pair the first device", "link", l)
	}
	if len(publics) > 0 {
		go proxied(ctx, *proxy)
	}
	if !a.Auth.Signed(ctx) && len(links) == 0 {
		slog.Info("no device is paired yet: a browser signs in only over https or at deck.localhost on this machine; start deck with -tailscale or -public")
	}
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, err
	}
	// Serve returns once Shutdown starts; the calls still running finish before deck closes.
	<-stopped
	return restart.Load(), nil
}

// cleanEnv restarts deck with only the variables it needs: on macOS a confined program reads the
// environment of every process of the owner.
func cleanEnv() error {
	clean := sandbox.Kept("HOME", "TMPDIR", "XDG_", "CODEX_HOME", "DECK_HOME", "DECK_ADDR", "DECK_TAILSCALE",
		"DECK_PUBLIC", "DECK_PROXY", "DECK_IMAGE")
	if len(clean) == len(os.Environ()) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, clean)
}

// pending starts the binary before an update when the updated deck already tried to start and never served; a
// first start of the update marks it tried.
func pending(h deckhome.Layout) error {
	p, ok := release.ReadPending(h.Update())
	switch {
	case !ok:
		return nil
	case !p.Tried:
		p.Tried = true
		return home.WriteFile(h.Update(), p.JSON())
	}
	slog.Error("the update did not start; deck goes back to the binary before it", "version", p.Version)
	os.Remove(h.Update())
	if err := os.Rename(p.Old, p.Exe); err != nil {
		return fmt.Errorf("go back from the update %s: %w", p.Version, err)
	}
	return syscall.Exec(p.Exe, os.Args, os.Environ())
}

// lockHome holds the lock of the home until deck exits; another deck holding it refuses this one.
func lockHome(h deckhome.Layout) (*os.File, error) {
	if err := os.MkdirAll(h.State(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(h.Lock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another deck runs on %s; one home has one deck", string(h))
	}
	return f, nil
}

// mcpBase is where agents of this machine reach deck's MCP servers: loopback when deck listens on every address, as
// the MCP handler refuses a Host such as 0.0.0.0.
func mcpBase(a *net.TCPAddr) string {
	if a.IP.IsUnspecified() {
		return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Port))
	}
	return "http://" + a.String()
}

// proxied says in the log when no proxy named by -proxy is found a while after the start, as Caddy of deck's compose
// with a domain but without its profile.
func proxied(ctx context.Context, list string) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	var names []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n == "" || net.ParseIP(n) != nil || strings.Contains(n, "/") {
			continue
		}
		if _, err := net.DefaultResolver.LookupHost(ctx, n); err == nil {
			return
		}
		names = append(names, n)
	}
	if len(names) > 0 {
		slog.Warn("no proxy before deck is found: with a domain in deck's compose put COMPOSE_PROFILES=domain into .env beside DECK_DOMAIN and run docker compose up -d", "proxy", strings.Join(names, ","))
	}
}

// url0 is the address deck names in its log: deck.localhost on this machine, else where a browser opens it.
func url0(origins *auth.Origins, ln net.Listener) string {
	for _, o := range origins.List() {
		if o.Here() {
			return o.Main
		}
	}
	return "http://" + opened(ln.Addr().(*net.TCPAddr))
}

// opened is the address a browser opens deck by: localhost when deck listens on every address.
func opened(a *net.TCPAddr) string {
	if a.IP.IsUnspecified() {
		return net.JoinHostPort("localhost", strconv.Itoa(a.Port))
	}
	return a.String()
}

// listenPair listens on addr and on the next port, the second origin of custom blocks; with any port it tries pairs
// until both are free.
func listenPair(addr string) (net.Listener, net.Listener, error) {
	for try := 0; ; try++ {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, nil, err
		}
		a := ln.Addr().(*net.TCPAddr)
		side, err := net.Listen("tcp", net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port+1)))
		if err == nil {
			return ln, side, nil
		}
		_ = ln.Close()
		if _, p, _ := net.SplitHostPort(addr); p != "0" || try == 20 {
			return nil, nil, fmt.Errorf("second origin for custom blocks: %w", err)
		}
	}
}

// Package mcphost starts the MCP servers deck calls tools of: on demand, again after a crash, all stopped
// together with deck.
package mcphost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/home"
	"deck2/internal/sandbox"
)

// Spec is how to reach a server: a program on stdio, or a URL.
type Spec struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
	URL     string
	// Read are folders of the owner a confined program reads.
	Read []string
	// Confine runs a program in the sandbox; Domains are what it reaches through deck's proxy, its only network.
	Confine bool
	Domains []string
	// Secrets are the secret values in Env; what the server writes to stderr shows without them.
	Secrets []string `json:"-"`
}

func (s Spec) hash() string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Status is what the host tells about a server after it started with its tools, failed to start or exited.
type Status func(key string, tools []*mcp.Tool, err error)

type Host struct {
	// StartTimeout bounds starting a server and listing its tools.
	StartTimeout time.Duration
	// CallTimeout bounds one tool call; a server that does not answer in time is stopped.
	CallTimeout time.Duration
	// Pause is how long a server that keeps crashing right after its start is not started again.
	Pause  time.Duration
	Now    func() time.Time
	Status Status
	// Sandbox confines programs of specs that ask for it; they never read Home, deck's folder, but for their own.
	Sandbox sandbox.Sandbox
	Home    home.Layout
	// Egress is deck's proxy of a confined server to its domains, taking requests that carry key; its tunnels end
	// with ctx.
	Egress func(ctx context.Context, key string, domains []string) http.Handler

	mu      sync.Mutex
	servers map[string]*server
	closed  bool
}

func New(status Status) *Host {
	return &Host{StartTimeout: 2 * time.Minute, CallTimeout: 2 * time.Minute, Pause: time.Minute, Now: time.Now, Status: status, servers: map[string]*server{}}
}

type server struct {
	mu      sync.Mutex
	spec    string
	sess    *mcp.ClientSession
	cmd     *exec.Cmd
	out     *http.Server
	cut     context.CancelFunc
	stderr  *tail
	secrets []string
	started time.Time
	// crashes counts exits soon after a start; paused holds starts back after too many.
	crashes int
	paused  time.Time
	tools   []*mcp.Tool
	// stale is set when the server says its tool list changed.
	stale atomic.Bool
}

// ErrNotStarted wraps why a call never reached its server.
type ErrNotStarted struct{ Err error }

func (e *ErrNotStarted) Error() string { return e.Err.Error() }
func (e *ErrNotStarted) Unwrap() error { return e.Err }

func (h *Host) server(key string) (*server, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("deck is stopping")
	}
	if h.servers[key] == nil {
		h.servers[key] = &server{}
	}
	return h.servers[key], nil
}

// Tools starts the server when it does not run with this spec and returns the tools it lists.
func (h *Host) Tools(ctx context.Context, key string, spec Spec) ([]*mcp.Tool, error) {
	s, err := h.server(key)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := h.ensure(ctx, key, s, spec); err != nil {
		return nil, err
	}
	return s.tools, nil
}

// Call makes one tool call after check saw the tools listed now. An error wrapping ErrNotStarted never
// reached the server, a *jsonrpc.Error is its answer, any other error may have been made.
func (h *Host) Call(ctx context.Context, key string, spec Spec, tool string, args json.RawMessage, check func([]*mcp.Tool) error) (*mcp.CallToolResult, error) {
	s, err := h.server(key)
	if err != nil {
		return nil, &ErrNotStarted{err}
	}
	s.mu.Lock()
	err = h.ensure(ctx, key, s, spec)
	if err == nil && check != nil {
		err = check(s.tools)
	}
	sess := s.sess
	s.mu.Unlock()
	if err != nil {
		return nil, &ErrNotStarted{err}
	}
	call, cancel := context.WithTimeout(ctx, h.CallTimeout)
	defer cancel()
	res, err := sess.CallTool(call, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil && ctx.Err() == nil && errors.Is(call.Err(), context.DeadlineExceeded) {
		s.mu.Lock()
		if s.sess == sess {
			s.stop()
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("MCP tool %s did not answer within %s; its server is started again on the next call", tool, h.CallTimeout)
	}
	return res, err
}

// Answered reports whether a call error is the server's own answer rather than a lost connection.
func Answered(err error) bool {
	var rpc *jsonrpc.Error
	return errors.As(err, &rpc)
}

// ensure runs with s.mu held; a server whose spec changed is stopped and started again.
func (h *Host) ensure(ctx context.Context, key string, s *server, spec Spec) error {
	want := spec.hash()
	if s.sess != nil && s.spec == want {
		if !s.stale.Swap(false) {
			return nil
		}
		err := s.list(ctx, key)
		if err == nil && h.Status != nil {
			h.Status(key, s.tools, nil)
		}
		return err
	}
	if s.sess != nil {
		s.stop()
	}
	if s.spec != want {
		s.crashes, s.paused = 0, time.Time{}
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return errors.New("deck is stopping")
	}
	if now := h.Now(); now.Before(s.paused) {
		return fmt.Errorf("MCP server %s crashed right after starting %d times; it is not started again before %s", key, s.crashes, s.paused.Format(time.TimeOnly))
	}
	s.spec = want
	start, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.StartTimeout)
	defer cancel()
	err := s.start(start, h, key, spec)
	if h.Status != nil {
		h.Status(key, s.tools, err)
	}
	return err
}

// Retire stops a running server whose spec is no longer spec, such as one with fewer domains now; the next call
// starts it as spec.
func (h *Host) Retire(key string, spec Spec) {
	h.mu.Lock()
	s := h.servers[key]
	h.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.sess != nil && s.spec != spec.hash() {
		s.stop()
	}
	s.mu.Unlock()
}

// Stop stops a server; the next call starts it again.
func (h *Host) Stop(key string) {
	h.mu.Lock()
	s := h.servers[key]
	h.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stop()
	s.mu.Unlock()
}

// Close stops every server; later calls fail.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	all := make([]*server, 0, len(h.servers))
	for _, s := range h.servers {
		all = append(all, s)
	}
	h.mu.Unlock()
	for _, s := range all {
		s.mu.Lock()
		s.stop()
		s.mu.Unlock()
	}
}

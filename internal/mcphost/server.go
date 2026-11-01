package mcphost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"deck2/internal/egress"
	"deck2/internal/untrusted"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/sandbox"
)

func (s *server) start(ctx context.Context, h *Host, key string, spec Spec) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "deck", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { s.stale.Store(true) }})
	var transport mcp.Transport
	s.proc = nil
	if spec.URL != "" {
		transport = &mcp.StreamableClientTransport{Endpoint: spec.URL, HTTPClient: egress.Client(egress.With(spec.Domains, spec.URL), 0)}
	} else {
		stderr := untrusted.NewTail(2048)
		p, err := h.Sandbox.Run(context.Background(), spec.Box, sandbox.IO{Stderr: func(line string) { _, _ = stderr.Write([]byte(line + "\n")) }})
		if err != nil {
			return fmt.Errorf("MCP server %s did not start: %v", key, err)
		}
		s.stderr, s.secrets, s.proc = stderr, spec.Secrets, p
		transport = &mcp.IOTransport{Reader: p.Stdout, Writer: p.Stdin}
	}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		s.kill()
		return fmt.Errorf("MCP server %s did not start: %v%s", key, err, s.last())
	}
	s.sess, s.started = sess, h.Now()
	if err := s.list(ctx, key); err != nil {
		s.stop()
		return err
	}
	go s.watch(h, key, sess)
	return nil
}

func (s *server) list(ctx context.Context, key string) error {
	var tools []*mcp.Tool
	for t, err := range s.sess.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("MCP server %s did not list its tools: %v%s", key, err, s.last())
		}
		tools = append(tools, t)
	}
	s.tools = tools
	return nil
}

// watch notices a server that exits by itself; it counts as a crash when it lived less than a minute.
func (s *server) watch(h *Host, key string, sess *mcp.ClientSession) {
	err := sess.Wait()
	s.mu.Lock()
	if s.sess != sess {
		s.mu.Unlock()
		return
	}
	s.sess = nil
	s.kill()
	if h.Now().Sub(s.started) < time.Minute {
		if s.crashes++; s.crashes >= 3 {
			s.paused = h.Now().Add(h.Pause)
		}
	} else {
		s.crashes = 0
	}
	text := s.last()
	s.mu.Unlock()
	if err == nil {
		err = errors.New("exited")
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if !closed && h.Status != nil {
		h.Status(key, nil, fmt.Errorf("MCP server %s stopped: %v%s; deck starts it again on the next call", key, err, text))
	}
}

func (s *server) last() string {
	if s.stderr == nil {
		return ""
	}
	if t := strings.TrimSpace(untrusted.Redact(s.stderr.String(), s.secrets)); t != "" {
		return "; stderr: " + t
	}
	return ""
}

// stop closes the session, which closes the stdin of a stdio server, and gives it 2 seconds to exit.
func (s *server) stop() {
	sess := s.sess
	s.sess = nil
	if sess != nil {
		_ = sess.Close()
	}
	if s.proc != nil {
		_ = s.proc.End(2 * time.Second)
	}
	s.proc = nil
}

// kill ends the box of a stdio server, with the programs it started and the tunnels of its door.
func (s *server) kill() {
	if s.proc != nil {
		s.proc.Kill()
		<-s.proc.Done()
	}
	s.proc = nil
}

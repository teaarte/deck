package mcphost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/untrusted"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/sandbox"
)

// command is the program of a stdio server; a confined one runs in the sandbox with the network, writing
// only its own folder and reading of the owner's home only the runtimes on PATH and the folders of its project.
func (h *Host) command(spec Spec, tag string) (*exec.Cmd, error) {
	if !spec.Confine || h.Sandbox == nil {
		cmd := exec.Command(spec.Command, spec.Args...)
		cmd.Env, cmd.Dir = spec.Env, spec.Dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd, nil
	}
	if err := h.Sandbox.Check(); err != nil {
		return nil, fmt.Errorf("sandbox %s: %w", h.Sandbox.Name(), err)
	}
	path, err := exec.LookPath(spec.Command)
	if err != nil {
		return nil, err
	}
	c := sandbox.Close(h.Home, env(spec.Env, "PATH")+string(os.PathListSeparator)+filepath.Dir(path))
	return h.Sandbox.Command(context.Background(), sandbox.Policy{Argv: append([]string{path}, spec.Args...), Dir: spec.Dir,
		Env: spec.Env, Write: []string{spec.Dir}, Read: append(slices.Clone(spec.Read), c.Read...), Hide: c.Hide, Private: c.Private, Net: true, Tag: tag})
}

// env is the value of the variable name in kv.
func env(kv []string, name string) string {
	for _, e := range kv {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v
		}
	}
	return ""
}

func (s *server) start(ctx context.Context, h *Host, key string, spec Spec) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "deck", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { s.stale.Store(true) }})
	var transport mcp.Transport
	s.cmd = nil
	if spec.URL != "" {
		transport = &mcp.StreamableClientTransport{Endpoint: spec.URL}
	} else {
		if spec.Dir != "" {
			if err := os.MkdirAll(spec.Dir, 0o700); err != nil {
				return err
			}
		}
		tag := "m" + key
		cmd, err := h.command(spec, tag)
		if err != nil {
			return fmt.Errorf("MCP server %s did not start: %v", key, err)
		}
		if spec.Confine && h.Sandbox != nil && h.Closed != nil {
			s.unwatch, _ = h.Sandbox.Watch(tag, func(path string) { h.Closed(key, path) })
		}
		s.stderr, s.secrets = &tail{}, spec.Secrets
		cmd.Stderr = s.stderr
		s.cmd = cmd
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}
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

func (s *server) stop() {
	sess := s.sess
	s.sess = nil
	if sess != nil {
		_ = sess.Close()
	}
	s.kill()
}

// kill ends the process group of a stdio server, with the programs it started.
func (s *server) kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
	s.cmd = nil
	if s.unwatch != nil {
		s.unwatch()
		s.unwatch = nil
	}
}

// tail keeps the last 2 KB a server wrote to stderr.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// message is one JSON-RPC line of the app-server: a response has an id and no method, a server request has both.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// conn speaks JSON-RPC over the app-server's stdin and stdout.
type conn struct {
	mu      sync.Mutex
	w       io.WriteCloser
	next    int
	pending map[string]chan message
	closed  bool
	// incoming gets notifications and server requests in order; it closes when the output ends.
	incoming chan message
	readErr  error
}

func newConn(w io.WriteCloser, r io.Reader) *conn {
	c := &conn{w: w, pending: map[string]chan message{}, incoming: make(chan message, 64)}
	go c.read(r)
	return c
}

func (c *conn) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" && len(m.ID) > 0 {
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			delete(c.pending, string(m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.incoming <- m
	}
	c.mu.Lock()
	c.readErr = sc.Err()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.incoming)
}

func (c *conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

// start sends a request and gives the channel its response arrives on.
func (c *conn) start(method string, params any) (<-chan message, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("codex app-server closed its output")
	}
	c.next++
	id := fmt.Sprint(c.next)
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"id": json.RawMessage(id), "method": method, "params": params}); err != nil {
		return nil, err
	}
	return ch, nil
}

// answer reads the response of a request into out; ok is false when the app-server exited first.
func answer(m message, ok bool, method string, out any) error {
	if !ok {
		return fmt.Errorf("codex app-server exited before answering %s", method)
	}
	if m.Error != nil {
		return fmt.Errorf("codex %s: %w", method, m.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(m.Result, out); err != nil {
		return fmt.Errorf("codex %s: %w", method, err)
	}
	return nil
}

func (c *conn) notify(method string, params any) error {
	m := map[string]any{"method": method}
	if params != nil {
		m["params"] = params
	}
	return c.write(m)
}

// reply answers a server request with a result, or with an error when text is set.
func (c *conn) reply(id json.RawMessage, result any, text string) error {
	if text != "" {
		return c.write(map[string]any{"id": id, "error": rpcError{Code: -32601, Message: text}})
	}
	return c.write(map[string]any{"id": id, "result": result})
}

func (c *conn) closeInput() { _ = c.w.Close() }

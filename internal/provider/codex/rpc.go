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

// conn speaks JSON-RPC over the app-server's stdin and stdout; lines to stdin queue and go out in their own
// goroutine, so the turn never stops reading while the app-server does not read.
type conn struct {
	mu      sync.Mutex
	w       io.WriteCloser
	next    int
	pending map[string]chan message
	closed  bool
	// incoming gets notifications and server requests in order; it closes when the output ends.
	incoming chan message
	readErr  error

	outMu   sync.Mutex
	outWake *sync.Cond
	out     [][]byte
	ending  bool
}

func newConn(w io.WriteCloser, r io.Reader) *conn {
	c := &conn{w: w, pending: map[string]chan message{}, incoming: make(chan message, 64)}
	c.outWake = sync.NewCond(&c.outMu)
	go c.read(r)
	go c.writeOut()
	return c
}

func (c *conn) writeOut() {
	defer c.w.Close()
	for {
		c.outMu.Lock()
		for len(c.out) == 0 && !c.ending {
			c.outWake.Wait()
		}
		lines, end := c.out, c.ending
		c.out = nil
		c.outMu.Unlock()
		for _, l := range lines {
			if _, err := c.w.Write(l); err != nil {
				c.outMu.Lock()
				c.ending = true
				c.outMu.Unlock()
				return
			}
		}
		if end {
			return
		}
	}
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
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if c.ending {
		return errors.New("codex app-server input is closed")
	}
	c.out = append(c.out, append(b, '\n'))
	c.outWake.Signal()
	return nil
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

// ended is the error of an output that stopped: what the reader could not read, or text.
func (c *conn) ended(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return fmt.Errorf("%s: its output could not be read: %w", text, c.readErr)
	}
	return errors.New(text)
}

// closeInput closes stdin once the queued lines are out.
func (c *conn) closeInput() {
	c.outMu.Lock()
	c.ending = true
	c.outWake.Signal()
	c.outMu.Unlock()
}

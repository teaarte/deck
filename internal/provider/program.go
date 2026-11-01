package provider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
)

// Killed is a program of a turn a signal ended that deck did not send: not a stop, the ceiling or a restart.
type Killed struct {
	Program string
	Signal  syscall.Signal
	Stderr  string
}

func (k *Killed) Error() string {
	text := fmt.Sprintf("%s was killed by %s from outside: not deck, a stop of the owner or the ceiling", k.Program, SignalName(k.Signal))
	if k.Signal == syscall.SIGKILL {
		text = fmt.Sprintf("%s; the system kills so when memory runs out", text)
	}
	return text
}

// KilledBy is the signal that ended a program by its exit error: a death by the signal, or the exit 128+n of a
// program that caught signal n and exited, as Claude Code does.
func KilledBy(err error) (syscall.Signal, bool) {
	var exit interface{ Sys() any }
	if !errors.As(err, &exit) {
		return 0, false
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	switch {
	case ok && ws.Signaled():
		return ws.Signal(), true
	case ok && ws.Exited():
		if sig := syscall.Signal(ws.ExitStatus() - 128); sig == syscall.SIGHUP || sig == syscall.SIGINT || sig == syscall.SIGKILL || sig == syscall.SIGTERM {
			return sig, true
		}
	}
	return 0, false
}

// Cut is the Killed of a program that exited before finishing its turn by a signal deck did not send; nil otherwise.
func Cut(program string, waitErr error, byDeck bool, stderr string) *Killed {
	if sig, ok := KilledBy(waitErr); ok && !byDeck {
		return &Killed{Program: program, Signal: sig, Stderr: stderr}
	}
	return nil
}

// SignalName is the name of a signal, such as SIGTERM.
func SignalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return fmt.Sprintf("signal %d", sig)
}

// Writer writes lines to the stdin of a program in order from a goroutine of its own, so a turn never waits
// on a program that does not read; Close closes stdin once the queued lines are out.
type Writer struct {
	mu     sync.Mutex
	wake   *sync.Cond
	w      io.WriteCloser
	out    []line
	ending bool
}

type line struct {
	b    []byte
	done chan error
}

func NewWriter(w io.WriteCloser) *Writer {
	q := &Writer{w: w}
	q.wake = sync.NewCond(&q.mu)
	go q.run()
	return q
}

// ErrClosed refuses a line for a stdin that is closed or failed.
var ErrClosed = errors.New("the input of the program is closed")

// Write queues a line; done gets the error of its write once it is out.
func (q *Writer) Write(b []byte) (done <-chan error, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ending {
		return nil, ErrClosed
	}
	ch := make(chan error, 1)
	q.out = append(q.out, line{b, ch})
	q.wake.Signal()
	return ch, nil
}

// Close closes stdin once the lines queued before it are out.
func (q *Writer) Close() {
	q.mu.Lock()
	q.ending = true
	q.wake.Signal()
	q.mu.Unlock()
}

func (q *Writer) run() {
	defer q.w.Close()
	for {
		q.mu.Lock()
		for len(q.out) == 0 && !q.ending {
			q.wake.Wait()
		}
		lines, end := q.out, q.ending
		q.out = nil
		q.mu.Unlock()
		for i, l := range lines {
			_, err := q.w.Write(l.b)
			l.done <- err
			if err != nil {
				q.mu.Lock()
				q.ending = true
				rest := append(lines[i+1:], q.out...)
				q.out = nil
				q.mu.Unlock()
				for _, r := range rest {
					r.done <- ErrClosed
				}
				return
			}
		}
		if end {
			return
		}
	}
}

// ReadSession is what a provider keeps of a deck session in a file; missing is a session never started.
func ReadSession(path string) (id string, missing bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", true, nil
	}
	return strings.TrimSpace(string(b)), false, err
}

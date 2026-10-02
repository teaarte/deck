package provider

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"deck2/internal/untrusted"
)

// Child is the program of a turn: a process group of its own, its stderr kept in a file until Close.
type Child struct {
	Cmd    *exec.Cmd
	stderr *os.File
}

// Start runs cmd in a process group of its own, unless it has one already, with its stderr in a file of dir;
// onStart gets its pid.
func Start(cmd *exec.Cmd, dir string, onStart func(pid int)) (*Child, error) {
	stderr, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		stderr.Close()
		os.Remove(stderr.Name())
		return nil, err
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}
	return &Child{Cmd: cmd, stderr: stderr}, nil
}

// Signal sends sig to the whole group of the program.
func (c *Child) Signal(sig syscall.Signal) { _ = syscall.Kill(-c.Cmd.Process.Pid, sig) }

// Stderr is what the program wrote to stderr, cut short.
func (c *Child) Stderr() string {
	b, _ := os.ReadFile(c.stderr.Name())
	return untrusted.Cut(strings.TrimSpace(string(b)), 2000)
}

// Close kills what is left of the group and removes the stderr file.
func (c *Child) Close() {
	c.Signal(syscall.SIGKILL)
	c.stderr.Close()
	os.Remove(c.stderr.Name())
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

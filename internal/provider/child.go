package provider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/sandbox"
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
func (c *Child) Signal(sig syscall.Signal) { _ = sandbox.Kill(c.Cmd, sig) }

// Stderr is the end of what the program wrote to stderr, where its error is.
func (c *Child) Stderr() string {
	b, _ := os.ReadFile(c.stderr.Name())
	return untrusted.Tail(strings.TrimSpace(string(b)), 2000)
}

// End waits for the program to exit once its protocol is over, its output read to the end first: up to linger, then a
// TERM, 5 seconds and a KILL of its group; the error is that of its exit.
func (c *Child) End(read <-chan struct{}, linger time.Duration) error {
	exited := make(chan error, 1)
	go func() {
		<-read
		exited <- c.Cmd.Wait()
	}()
	for _, step := range []struct {
		wait time.Duration
		sig  syscall.Signal
	}{{linger, syscall.SIGTERM}, {5 * time.Second, syscall.SIGKILL}, {5 * time.Second, 0}} {
		select {
		case err := <-exited:
			return err
		case <-time.After(step.wait):
		}
		if step.sig == 0 {
			return errors.New("the program did not end after its group was killed")
		}
		c.Signal(step.sig)
	}
	return nil
}

// Close kills what is left of the group and removes the stderr file.
func (c *Child) Close() {
	c.Signal(syscall.SIGKILL)
	c.stderr.Close()
	os.Remove(c.stderr.Name())
}

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
	var exit *exec.ExitError
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

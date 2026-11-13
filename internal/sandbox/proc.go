package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Proc is the program of a box running: a process group of its own, ended whole with the processes it started once
// the program exits.
type Proc struct {
	// Stdin and Stdout are pipes to the program when its IO gave none.
	Stdin  io.WriteCloser
	Stdout io.ReadCloser

	pid    int
	signal func(syscall.Signal)
	door   *Door
	done   chan struct{}
	err    error
	killed atomic.Bool
}

// Remote is the Proc of a program that runs on another machine, such as a node: its stdin, its signal, and the error
// of its end once done has it.
func Remote(stdin io.WriteCloser, signal func(syscall.Signal), done <-chan error) *Proc {
	p := &Proc{Stdin: stdin, signal: signal, done: make(chan struct{})}
	go func() {
		p.err = <-done
		close(p.done)
	}()
	return p
}

func start(cmd *exec.Cmd, door *Door, o IO, secrets []string) (*Proc, error) {
	p := &Proc{door: door, done: make(chan struct{})}
	theirs := slices.Clone(cmd.ExtraFiles)
	fail := func(err error) (*Proc, error) {
		for _, f := range theirs {
			_ = f.Close()
		}
		door.Close()
		return nil, err
	}
	if o.Stdin != nil {
		cmd.Stdin = o.Stdin
	} else {
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		cmd.Stdin, p.Stdin, theirs = r, w, append(theirs, r)
	}
	var copies []*copier
	if o.Stdout == nil {
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		cmd.Stdout, p.Stdout, theirs = w, readerOf(r, masking(secrets)), append(theirs, w)
	} else {
		c, w, err := pipe(writerOf(o.Stdout, masking(secrets)))
		if err != nil {
			return fail(err)
		}
		cmd.Stdout, theirs, copies = w, append(theirs, w), append(copies, c)
	}
	c, w, err := pipe(writerOf(&lines{f: o.Stderr}, masking(secrets)))
	if err != nil {
		return fail(err)
	}
	cmd.Stderr, theirs, copies = w, append(theirs, w), append(copies, c)
	end := descend(cmd)
	err = cmd.Start()
	if err == nil {
		p.pid = cmd.Process.Pid
		p.signal = func(sig syscall.Signal) { _ = syscall.Kill(-p.pid, sig) }
	}
	for _, f := range theirs {
		_ = f.Close()
	}
	if err != nil {
		for _, c := range copies {
			c.close()
		}
		door.Close()
		return nil, err
	}
	go func() {
		p.err = cmd.Wait()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		end()
		late, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		for _, c := range copies {
			c.drain(late)
		}
		cancel()
		door.Close()
		close(p.done)
	}()
	return p, nil
}

// Pid is the process id of the box, the id of its group; 0 for one on another machine.
func (p *Proc) Pid() int { return p.pid }

// Done is closed once the program exited, its group is killed and its output is in.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Wait waits for Done and gives the error of the program's exit.
func (p *Proc) Wait() error {
	<-p.done
	return p.err
}

// Signal sends sig to the whole group of the box while it runs; once it is done, its group id may be another's.
func (p *Proc) Signal(sig syscall.Signal) {
	select {
	case <-p.done:
		return
	default:
	}
	if sig == syscall.SIGTERM || sig == syscall.SIGKILL {
		p.killed.Store(true)
	}
	p.signal(sig)
}

// Kill ends the box at once.
func (p *Proc) Kill() { p.Signal(syscall.SIGKILL) }

// Killed says deck ended the box rather than its program exiting by itself.
func (p *Proc) Killed() bool { return p.killed.Load() }

// End waits up to linger for the program to exit, then sends TERM, 5 seconds later KILL; the error is that of its exit.
func (p *Proc) End(linger time.Duration) error {
	for _, step := range []struct {
		wait time.Duration
		sig  syscall.Signal
	}{{linger, syscall.SIGTERM}, {5 * time.Second, syscall.SIGKILL}, {5 * time.Second, 0}} {
		select {
		case <-p.done:
			return p.err
		case <-time.After(step.wait):
		}
		if step.sig == 0 {
			return errors.New("the program did not end after its box was killed")
		}
		p.Signal(step.sig)
	}
	return nil
}

// URL is the address the program reaches deck at; empty without a door.
func (p *Proc) URL() string {
	if p.door == nil {
		return ""
	}
	return p.door.URL()
}

// Refused are the reasons the door turned the program's requests away, the first few.
func (p *Proc) Refused() []string {
	if p.door == nil {
		return nil
	}
	return p.door.Refused()
}

// ExitCode is the code a program exited with, when err is such an exit.
func ExitCode(err error) (int, bool) {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), true
	}
	return 0, false
}

// copier carries what a program writes to w until its drain; a program it started may hold the pipe longer.
type copier struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
	done   chan struct{}
}

func pipe(w io.Writer) (*copier, *os.File, error) {
	r, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	c := &copier{w: w, done: make(chan struct{})}
	go func() { _, _ = io.Copy(c, r); _ = r.Close(); close(c.done) }()
	return c, pw, nil
}

func (c *copier) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return len(b), nil
	}
	return c.w.Write(b)
}

// drain takes what comes until late is done, then drops the rest.
func (c *copier) drain(late context.Context) {
	select {
	case <-c.done:
	case <-late.Done():
	}
	c.close()
}

func (c *copier) close() {
	c.mu.Lock()
	c.closed = true
	if f, ok := c.w.(flusher); ok {
		f.flush()
	}
	c.mu.Unlock()
}

// lines passes each line written to f.
type lines struct {
	f   func(string)
	buf []byte
}

func (l *lines) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.line(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 64<<10 {
		l.flush()
	}
	return len(p), nil
}

func (l *lines) flush() {
	if len(l.buf) > 0 {
		l.line(string(l.buf))
		l.buf = nil
	}
}

func (l *lines) line(s string) {
	if l.f != nil {
		l.f(s)
	}
}

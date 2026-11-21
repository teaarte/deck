package harness

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

var T0 = time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)

// Tick runs one turn of deck's loop at the clock of the test.
func (e *Env) Tick() {
	e.App.Tick(context.Background(), e.App.Store.Now())
}

// Schedule starts what the triggers have due, the first step of a tick alone.
func (e *Env) Schedule() {
	e.App.Scheduler.Tick(context.Background())
}

// Clock is the deck clock of a test: it stands still until the test moves it.
type Clock struct {
	mu sync.Mutex
	At time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.At
}

func (c *Clock) Add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.At = c.At.Add(d)
	return c.At
}

// PassWaits moves the clock past every wait for the owner until ok holds.
func (e *Env) PassWaits(what string, ok func() bool) {
	e.T.Helper()
	e.Until(what, func() bool {
		if ok() {
			return true
		}
		e.Clock.Add(e.App.Cfg.QuestionWait + e.App.Cfg.ConfirmWait)
		return false
	})
}

// PassState moves the clock past every wait for the owner until process id is in one of states.
func (e *Env) PassState(id int64, states ...string) Proc {
	e.T.Helper()
	var p Proc
	e.PassWaits(fmt.Sprintf("process %d to reach %v", id, states), func() bool {
		p = e.Proc(id)
		return slices.Contains(states, p.State)
	})
	return p
}

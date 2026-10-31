package harness

import (
	"context"
	"time"
)

// Heard waits for a message of the owner on the live channel of a turn and takes it.
func Heard(ctx context.Context, c *Call) string {
	select {
	case m := <-c.Req.Live:
		m.Taken <- true
		return m.Text
	case <-ctx.Done():
	case <-time.After(Within(10 * time.Second)):
	}
	return ""
}

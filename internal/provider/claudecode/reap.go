package claudecode

import "deck2/internal/sandbox"

// Reap kills the group only when its leader is gone or still runs the session.
func (a *Adapter) Reap(pgid int, session string) { sandbox.Reap(pgid, session) }

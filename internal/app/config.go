package app

import (
	"time"

	"deck2/internal/sandbox"
)

type Config struct {
	Home         string
	MCPBase      string
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	ConfirmTTL   time.Duration
	// Now is the deck clock; every deadline is written and checked by it.
	Now func() time.Time
	// Ceiling is how long a command agent may work unless its manifest says otherwise.
	Ceiling time.Duration
	// SilentAfter is how long a running process may go without output before it shows as silent.
	SilentAfter time.Duration
	// CatchUpSpread is the gap between runs missed during a sleep that start one after another.
	CatchUpSpread time.Duration
	// EmptyKept is how long a process with an empty result stays in the history.
	EmptyKept time.Duration
	// ScriptCeiling is how long a command script may run unless its manifest says otherwise.
	ScriptCeiling time.Duration
	// Snapshots is how many daily snapshots stay in backups/.
	Snapshots int
	// Sandbox confines scripts; the one of this operating system when nil.
	Sandbox sandbox.Sandbox
	// Browser starts the browser MCP server; DefaultBrowser when nil, none when empty.
	Browser []string
	// PauseLimit is how long one pause on a usage limit lasts at most; PauseProbe is the try without a reset time.
	PauseLimit time.Duration
	PauseProbe time.Duration
	// SelfCheckCeiling bounds a self-check of the main provider; SelfCheckRetry repeats a red one.
	SelfCheckCeiling time.Duration
	SelfCheckRetry   time.Duration
	// VersionEvery is how often deck looks for a new version of the main provider's program.
	VersionEvery time.Duration
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Ceiling <= 0 {
		c.Ceiling = time.Hour
	}
	if c.SilentAfter <= 0 {
		c.SilentAfter = 30 * time.Minute
	}
	if c.CatchUpSpread <= 0 {
		c.CatchUpSpread = time.Minute
	}
	if c.EmptyKept <= 0 {
		c.EmptyKept = 24 * time.Hour
	}
	if c.ScriptCeiling <= 0 {
		c.ScriptCeiling = 10 * time.Minute
	}
	if c.Snapshots <= 0 {
		c.Snapshots = 7
	}
	if c.Sandbox == nil {
		c.Sandbox = sandbox.New()
	}
	if c.PauseLimit <= 0 {
		c.PauseLimit = 6 * time.Hour
	}
	if c.PauseProbe <= 0 {
		c.PauseProbe = time.Hour
	}
	if c.SelfCheckCeiling <= 0 {
		c.SelfCheckCeiling = 2 * time.Minute
	}
	if c.SelfCheckRetry <= 0 {
		c.SelfCheckRetry = 30 * time.Minute
	}
	if c.VersionEvery <= 0 {
		c.VersionEvery = 10 * time.Minute
	}
	if c.Browser == nil {
		c.Browser = DefaultBrowser
	}
}

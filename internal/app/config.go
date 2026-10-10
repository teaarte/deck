package app

import (
	"deck2/internal/registry"
	"time"

	"deck2/internal/sandbox"
)

type Config struct {
	Home         string
	MCPBase      string
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	// ConfirmTTL is how long a confirmation waits unless the owner set it; zero keeps it open.
	ConfirmTTL time.Duration
	// Now is the deck clock; every deadline is written and checked by it.
	Now func() time.Time
	// Sandbox confines scripts and ACP agents; the one of this operating system when nil.
	Sandbox sandbox.Sandbox
	// ACPBinaries are the programs of ACP agents by agent name; an agent missing here is looked up in PATH.
	ACPBinaries map[string]string
	// ACPCallLimit is how long a call of an ACP agent may wait for the owner before its turn stops; zero keeps 45 s.
	ACPCallLimit time.Duration
	// Browser starts the browser MCP server; DefaultBrowser when nil, none when empty.
	Browser []string
	// SelfCheckCeiling bounds a self-check of a built-in provider.
	SelfCheckCeiling time.Duration
	// VideoPoll is how often deck asks after a job of a video model, VideoCeiling how long it waits for one.
	VideoPoll, VideoCeiling time.Duration
	// Progress is how often a long call of a tool tells an agent that asked for it that it still runs.
	Progress time.Duration
	// SnapshotBudget is how much the snapshots take together; 2 GB when zero.
	SnapshotBudget int64
	// Commands are more builtin commands, such as the probes of tests.
	Commands []*registry.Command
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Sandbox == nil {
		c.Sandbox = sandbox.New()
	}
	if c.SelfCheckCeiling <= 0 {
		c.SelfCheckCeiling = 2 * time.Minute
	}
	if c.VideoPoll <= 0 {
		c.VideoPoll = 10 * time.Second
	}
	if c.VideoCeiling <= 0 {
		c.VideoCeiling = 30 * time.Minute
	}
	if c.Progress <= 0 {
		c.Progress = 30 * time.Second
	}
	if c.SnapshotBudget <= 0 {
		c.SnapshotBudget = 2 << 30
	}
	if c.Browser == nil {
		c.Browser = DefaultBrowser
	}
}

// DefaultBrowser starts Playwright MCP of a known version, so the tools deck sorts into reading and acting
// stay the ones it knows.
var DefaultBrowser = []string{"npm", "exec", "--yes", "--package=@playwright/mcp@0.0.82", "--", "playwright-mcp"}

package app

import (
	"os"
	"time"

	"deck2/internal/app/updates"
	"deck2/internal/auth"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/release"
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
	// Sandbox runs every foreign program in its box; the one of this machine when nil.
	Sandbox *sandbox.Sandbox
	// ACPBinaries are the programs of ACP agents by agent name; an agent missing here is looked up in PATH.
	ACPBinaries map[string]string
	// ACPCallLimit is how long a call of an ACP agent may wait for the owner before its turn stops; zero keeps 45 s.
	ACPCallLimit time.Duration
	// Browser starts the browser MCP server; DefaultBrowser when nil, none when empty.
	Browser []string
	// SelfCheckCeiling bounds a self-check of a built-in provider.
	SelfCheckCeiling time.Duration
	// NodeWait is how long a turn whose node went away waits for it before it goes to the next agent; 3 minutes when
	// zero.
	NodeWait time.Duration
	// VideoPoll is how often deck asks after a job of a video model, VideoCeiling how long it waits for one.
	VideoPoll, VideoCeiling time.Duration
	// Progress is how often a long call of a tool tells an agent that asked for it that it still runs.
	Progress time.Duration
	// SnapshotBudget is how much the snapshots take together; 2 GB when zero.
	SnapshotBudget int64
	// Commands are more builtin commands, such as the probes of tests.
	Commands []*registry.Command
	// Releases is where deck finds its updates; Version and Variant are this build's, and Restart ends deck to take
	// an update.
	Releases updates.Source
	Version  string
	Variant  string
	// Image is the container image deck runs from (DECK_IMAGE), which updates by a newer image.
	Image   string
	Restart func(release.Restart)
	// Exe is the binary of deck an update replaces; the running one when nil.
	Exe func() (string, error)
	// Origins are the addresses deck signs the owner in at, each with its second origin; none when nil.
	Origins *auth.Origins
}

// wall is the clock of a deck given none.
var wall = time.Now

// PairCode writes a code that pairs a device into a home, as deck pair does whether deck runs or not.
func PairCode(h home.Layout, kind, name string) (string, time.Time, error) {
	return auth.WriteCode(h, wall(), 0, kind, name)
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = wall
	}
	if c.Origins == nil {
		c.Origins = auth.NewOrigins()
	}
	if c.Sandbox == nil {
		c.Sandbox = sandbox.New(home.Layout(c.Home))
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
	if c.Releases.URL == "" {
		c.Releases = updates.GitHub
	}
	if c.Releases.Key == nil {
		c.Releases.Key = release.Key()
	}
	if c.Version == "" {
		c.Version = release.Version
	}
	if c.Variant == "" {
		c.Variant = release.Variant(false)
	}
	if c.Exe == nil {
		c.Exe = os.Executable
	}
	if c.Restart == nil {
		c.Restart = func(release.Restart) {}
	}
	if c.Browser == nil {
		c.Browser = DefaultBrowser
	}
}

// DefaultBrowser starts Playwright MCP of a known version, so the tools deck sorts into reading and acting
// stay the ones it knows.
var DefaultBrowser = []string{"npm", "exec", "--yes", "--package=@playwright/mcp@0.0.82", "--", "playwright-mcp"}

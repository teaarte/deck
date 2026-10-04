package app

import (
	"deck2/internal/app/devices"
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
	// PauseLimit is how long one pause on a usage limit lasts at most.
	PauseLimit time.Duration
	// SelfCheckCeiling bounds a self-check of a built-in provider.
	SelfCheckCeiling time.Duration
	// STT is the speech-to-text url unless the owner set one; devices.DefaultSTT when empty.
	STT string
	// Commands are more builtin commands, such as the probes of tests.
	Commands []*registry.Command
}

const (
	// silentAfter is how long a running process may go without output before it shows as silent.
	silentAfter = 30 * time.Minute
	// emptyKept is how long a process with an empty result stays in the history.
	emptyKept = 24 * time.Hour
	// snapshotsKept is how many daily snapshots stay in backups/.
	snapshotsKept = 7
	// workKept is how long the work folder of a finished agent process stays for rework.
	workKept = 7 * 24 * time.Hour
)

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Sandbox == nil {
		c.Sandbox = sandbox.New()
	}
	if c.PauseLimit <= 0 {
		c.PauseLimit = 6 * time.Hour
	}
	if c.SelfCheckCeiling <= 0 {
		c.SelfCheckCeiling = 2 * time.Minute
	}
	if c.Browser == nil {
		c.Browser = DefaultBrowser
	}
	if c.STT == "" {
		c.STT = devices.DefaultSTT
	}
}

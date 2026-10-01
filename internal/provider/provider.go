package provider

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

type Request struct {
	Cwd string
	// Repo means Cwd is a code repository: its own instructions load, those of the folders above do not.
	Repo         bool
	Prompt       string
	ResultSchema json.RawMessage
	// Skills are AgentSkills files by path, "<skill>/SKILL.md" and the rest of each skill folder.
	Skills map[string]string
	MCPURL string
	// Hidden are path patterns in the permission rule syntax the agent may not read or write, such as secrets.
	Hidden []string
	// Dirs are folders outside Cwd the agent also writes to, such as the data folder of the project.
	Dirs []string
	// NoNetwork takes away the provider's own web and the network of its shell.
	NoNetwork     bool
	AllowedTools  []string
	PermissionMCP string
	// SessionID is assigned by deck; Resume continues it instead of starting it.
	SessionID string
	Resume    bool
	OnEvent   func(Event)
	// OnUsage gets the token counts of each model call as the provider reports them.
	OnUsage func(Usage)
	// OnStart gets the id of the process group the provider runs in, so a later deck can kill it.
	OnStart func(pgid int)
	// Live brings the owner's messages into the running turn, for a provider with live input.
	Live <-chan Message
}

// Message is the owner's message into a running turn; the provider tells on Taken whether the turn took it.
type Message struct {
	Text  string
	Taken chan<- bool
}

// Event is an event of the process itself in the words of ACP session/update; events of the provider's
// sub-agents are not reported.
type Event struct {
	Kind string
	// ID ties a tool call to its update.
	ID    string
	Title string
	// Status of a tool update: completed or failed.
	Status string
	Text   string
}

const (
	EventMessage    = "message"
	EventThought    = "thought"
	EventTool       = "tool"
	EventToolUpdate = "tool_update"
	EventRateLimit  = "rate_limit"
	EventStderr     = "stderr"
)

// ToolStatus maps the end state of a tool call to completed or failed.
func ToolStatus(ok bool) string {
	if ok {
		return "completed"
	}
	return "failed"
}

type Result struct {
	// Structured is the result by the schema; JSON null means there was nothing to do.
	Structured json.RawMessage
	Text       string
	IsError    bool
	// NeedsLogin is set when the turn failed because the provider's login is missing, expired or refused.
	NeedsLogin bool
	// SessionMissing is set when a resume found no stored session, so the session has to start over.
	SessionMissing bool
	// RateLimited is set when the provider refused on a subscription limit; ResetsAt is unix seconds.
	RateLimited bool
	ResetsAt    int64
	// Denied are the tools the provider refused by its own rules or auto mode, one entry per refused call.
	Denied []string
}

// Usage is one model call; the counts come from the final event of a turn or call, not from summed messages.
type Usage struct {
	// Model is the model that answered; RequestModel the one deck asked for, when it names one.
	Model         string
	RequestModel  string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
	Duration      time.Duration
	// CostUSD is what the provider reports, or nil; on a subscription it is notional.
	CostUSD *float64
}

// Capabilities a manifest command may require of its provider.
const (
	Tools = "tools"
	// Sandbox is a shell and file writes in the provider's auto mode: it passes what is safe and asks about the rest.
	Sandbox   = "sandbox"
	Resume    = "resume"
	Intercept = "intercept"
	Images    = "images"
	NoCloud   = "no_cloud"
	// Web is the provider's own web search and fetch.
	Web = "web"
	// Folder is working in a folder of the owner, a repo with its own instructions, rather than a folder of deck.
	Folder = "folder"
	// LiveInput takes the owner's messages into a running turn.
	LiveInput = "live"
)

// Server is the name of the deck MCP server among the MCP servers of a provider session.
const Server = "deck"

// AskTool is the gateway tool a provider without a question tool of its own asks the owner with.
const AskTool = "mcp__" + Server + "__ask"

// StructuredOutput is output by schema; a provider without it hands the result in with result_submit.
const StructuredOutput = "structured_output"

// Caps lists the capabilities a provider has.
type Caps []string

// Missing are the required capabilities the provider does not have.
func (c Caps) Missing(requires []string) []string {
	var out []string
	for _, r := range requires {
		if !slices.Contains(c, r) {
			out = append(out, r)
		}
	}
	return out
}

type Provider interface {
	Name() string
	Caps() Caps
	// QuestionTool names the provider tool that asks the owner a blocking question, or is empty.
	QuestionTool() string
	Run(ctx context.Context, req Request) (Result, error)
}

// Check is what a self-check of a provider found; Err is nil when the adapter works.
type Check struct {
	Version     string
	Err         error
	RateLimited bool
	NeedsLogin  bool
	// ModelFailed is set when Err is the model not doing the steps of the check, not a fault of the adapter.
	ModelFailed bool
	// Speed is the output tokens per second of the model over the check, zero when not measured.
	Speed float64
}

// SelfChecker is a provider whose adapter deck checks at start and when the version of its program changes.
type SelfChecker interface {
	Version(ctx context.Context) (string, error)
	SelfCheck(ctx context.Context) Check
}

// CallLimiter is a provider whose calls of gateway tools time out: a wait for the owner ends before that and the
// turn stops, so the answer comes by resume.
type CallLimiter interface {
	CallLimit() time.Duration
}

// Reaper kills a process group an earlier deck left running, if it still runs the given session.
type Reaper interface {
	Reap(pgid int, session string)
}

// Terminal is a provider whose session the owner can go on with in a terminal: the command, in the folder of the turn.
type Terminal interface {
	Terminal(session string) (string, error)
}

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/sandbox"
)

type Request struct {
	// Project is the project of the turn, whose home of the CLI the turn runs in.
	Project int64
	Cwd     string
	// Repo means Cwd is a code repository: its own instructions load, those of the folders above do not.
	Repo bool
	// Folder means Cwd is a folder of the project, on the machine of the provider.
	Folder       bool
	Prompt       string
	ResultSchema json.RawMessage
	// Skills are AgentSkills files by path, "<skill>/SKILL.md" and the rest of each skill folder.
	Skills map[string]string
	// MCPURL is deck's MCP server of the turn; the program reaches it through the door of its box.
	MCPURL string
	// Dirs are folders outside Cwd the agent also writes to, such as the data folder of the project.
	Dirs []string
	// Data is the data folder of the project, where a confined shell keeps the caches of its programs.
	Data string
	// Read are folders the agent reads besides its own: the folders of its project.
	Read []string
	// Given are folders of deck's work the agent reads and does not write: the files given with its messages.
	Given []string
	// Hosts are what the shell reaches besides the provider's own: hosts with their subdomains, host:port, IPs; "*"
	// is every public one, none is only the provider and deck.
	Hosts []string
	// GPU opens the graphics card to the shell.
	GPU           bool
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
	// EventDenied is a call the agent's own permission rules or auto mode refused, with why.
	EventDenied = "denied"
	// EventWarning is deck's word to the owner about the turn, in the feed too; Title is the health row it is of.
	EventWarning = "warning"
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
	// Fault is how a turn failed, nil when it ended well: task.failed, model.login, model.limit, model.unavailable, or
	// deck.tools for an agent that stopped because deck's tools did not reach it.
	Fault *fault.F
	// Begun is a turn without deck's tools whose session the CLI had opened, so it goes on by a resume.
	Begun bool
	// SessionMissing is a resume that found no stored session, so the session starts over.
	SessionMissing bool
	// ResetsAt is when a usage limit ends, in unix seconds.
	ResetsAt int64
	// Denied are the tools the provider refused by its own rules or auto mode, one entry per refused call.
	Denied []string
	// Refusal is the status and words of the model API when it refused the last call of the turn as a bad request.
	Refusal string
	// Past is why deck did not send the last call of the turn: it was past a limit of the model in deck's settings.
	Past string
}

// Is reports whether the turn failed with type t.
func (r Result) Is(t string) bool { return r.Fault != nil && r.Fault.Type == t }

// Held is the type of a failure that holds the provider and not only the turn: a usage limit or a lost login, else
// empty.
func (r Result) Held() string {
	if r.Is(fault.ModelLimit) || r.Is(fault.ModelLogin) {
		return r.Fault.Type
	}
	return ""
}

// Fail is a result of a turn that failed with type t and text, its words too.
func Fail(t, text string) Result { return Result{Fault: fault.New(t, text), Text: text} }

// Fail marks the result failed with type t in its words.
func (r *Result) Fail(t string) { r.Fault = fault.New(t, r.Text) }

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
	// CostUSD is what the provider reports, or nil; on a subscription it is notional. SessionCostUSD is what the whole
	// session cost so far, for a provider that counts the session and not the turn.
	CostUSD        *float64
	SessionCostUSD *float64
	// Chars are the characters of the input deck sent, Seconds the length of a recording the API reported.
	Chars   int64
	Seconds float64
	// Connection is the connection of deck's settings the call went through; zero for a provider's own.
	Connection int64
}

// Capabilities a manifest command may require of its provider.
const (
	Images  = "images"
	NoCloud = "no_cloud"
	// LiveInput takes the owner's messages into a running turn.
	LiveInput = "live"
)

// Server is the name of the deck MCP server among the MCP servers of a provider session.
const Server = "deck"

// AskTool is the gateway tool a provider without a question tool of its own asks the owner with.
const AskTool = "mcp__" + Server + "__ask"

// Qualify is the name a gateway tool has among the tools of a provider session; Bare is the name on the server.
func Qualify(tool string) string { return "mcp__" + Server + "__" + tool }

func Bare(tool string) string { return strings.TrimPrefix(tool, "mcp__"+Server+"__") }

// Emit hands an event to the request's listener, when it has one.
func (r Request) Emit(e Event) {
	if r.OnEvent != nil {
		r.OnEvent(e)
	}
}

// PastLimit marks deck's own refusal of a call of a model past a limit in its settings: no model got the call.
const PastLimit = "X-Deck-Past-Limit"

// Refused marks a result by the answer deck's proxy gave a call of a model: a usage limit pauses until the end
// LimitEnds reads, a refused key needs a login, a fault is a model that did not answer; false for any other status.
func Refused(res *Result, a Answer, now time.Time) bool {
	switch fault.OfStatus(a.Status, true) {
	case fault.ModelLimit:
		res.Fail(fault.ModelLimit)
		res.ResetsAt = LimitEnds(a.Header, a.Words, now)
	case fault.ModelLogin:
		res.Fail(fault.ModelLogin)
	case fault.ModelUnavailable:
		res.Fail(fault.ModelUnavailable)
	default:
		return false
	}
	return true
}

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
	Version string
	Err     error
	// Held is model.limit or model.login when the check hit a usage limit or found no valid login.
	Held string
	// ModelFailed is set when Err is the model not doing the steps of the check, not a fault of the adapter.
	ModelFailed bool
	// Slow is set when the model ran out of time rather than failed a step.
	Slow bool
	// Speed is the output tokens per second of the model over the check, zero when not measured.
	Speed float64
}

// Subscribed is a provider that runs on the owner's subscription login, the one OnLogin names: what its calls would
// cost is only an estimate.
type Subscribed interface {
	OnLogin() string
}

// AtNode splits the name of a provider on a node, such as claude-code@Mac, into the provider and the node.
func AtNode(name string) (base, node string, ok bool) {
	i := strings.LastIndex(name, "@")
	if i <= 0 || i == len(name)-1 {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

// Noded is a provider whose turns run on a node, by the device of the node.
type Noded interface {
	NodeOf() int64
}

// Model is a model a CLI lists: the names it answers to, the efforts it takes, and why deck's turns cannot run on
// it, empty when they can.
type Model struct {
	Names   []string
	Efforts []string
	Refused string
}

// Modeled is a built-in provider whose CLI runs other models: Models are those the CLI lists, With is the provider
// on one of them under the name deck's settings give it.
type Modeled interface {
	Models(ctx context.Context) ([]Model, error)
	With(name, model, effort string) Provider
}

// Installed is a provider whose program may be missing from the machine: Install is the command that installs it.
type Installed interface {
	Installed() bool
	Install() string
}

// Tooled is a provider whose agent adds tools of its own to each request beside deck's.
type Tooled interface {
	OwnTools() int
}

// Tested is a provider of a CLI whose version CI tests deck with; another version runs, with a warning.
type Tested interface {
	TestedVersion() string
}

// SelfChecker is a provider whose adapter deck checks at start, on a button and on a broken turn.
type SelfChecker interface {
	SelfCheck(ctx context.Context) Check
}

// SignIn is a CLI the owner signs in to from deck: Login starts its program of a sign-in by a link, which prints the
// link and a code, or waits on stdin for the code the page of the link gives.
type SignIn interface {
	Login(ctx context.Context, o sandbox.IO) (*sandbox.Proc, error)
}

// Mover is a CLI whose sessions live in the homes of projects: Move copies a session of deck that may go on from the
// user's own home of the CLI into the home of its project, once; a session it finds no files of stays as it is.
type Mover interface {
	Move(project int64, session string) error
}

// CallLimiter is a provider whose calls of gateway tools time out: a wait for the owner ends before that and the
// turn stops, so the answer comes by resume.
type CallLimiter interface {
	CallLimit() time.Duration
}

// Terminal is a provider whose session the owner can go on with in a terminal: the command, in the folder of the turn.
type Terminal interface {
	Terminal(project int64, session string) (string, error)
}

// Answer is what deck's proxy of a model answered a call of a turn: the status, its header, Past for deck's own
// refusal past a limit, and the words of a refusal.
type Answer struct {
	Status int
	Header http.Header
	Past   bool
	Words  string
}

// ModelHooks hear what deck's proxy of the model of a turn did: the usage of each call and each answer.
type ModelHooks struct {
	Usage  func(Usage)
	Answer func(Answer)
}

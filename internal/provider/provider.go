package provider

import (
	"context"
	"database/sql"
	"deck2/internal/fault"
	"encoding/json"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"
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
	// Data is the data folder of the project, where a confined shell keeps the caches of its programs.
	Data string
	// Read are folders the agent reads where its own sandbox closes the home folder: the folders of its project.
	Read []string
	// NoNetwork takes away the provider's own web and the network of its shell.
	NoNetwork bool
	// Hosts are what the shell reaches: hosts with their subdomains, host:port, IPs; "*" is every one.
	Hosts         []string
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
)

// ToolStatus maps the end state of a tool call to completed or failed.
func ToolStatus(ok bool) string {
	if ok {
		return "completed"
	}
	return "failed"
}

// Outcome is how a turn ended, of a closed set; Done is a turn that ended well.
type Outcome string

const (
	Done Outcome = ""
	// Failed is a turn the agent or its CLI ended with an error.
	Failed Outcome = "failed"
	// Login is a turn that failed because the provider's login is missing, expired or refused.
	Login Outcome = "login"
	// Limited is a refusal on a usage limit; Result.ResetsAt is when it ends.
	Limited Outcome = "limited"
	// Unavailable is a model or its API that did not answer: the network, a 5xx, an overloaded model.
	Unavailable Outcome = "unavailable"
	// SessionMissing is a resume that found no stored session, so the session starts over.
	SessionMissing Outcome = "session_missing"
	// NoTools is an agent that stopped before its first model call because deck's MCP tools did not reach it.
	NoTools Outcome = "no_tools"
)

type Result struct {
	// Structured is the result by the schema; JSON null means there was nothing to do.
	Structured json.RawMessage
	Text       string
	Outcome    Outcome
	// ResetsAt is when a usage limit ends, in unix seconds.
	ResetsAt int64
	// Denied are the tools the provider refused by its own rules or auto mode, one entry per refused call.
	Denied []string
	// Refusal is the status and words of the model API when it refused the last call of the turn as a bad request.
	Refusal string
	// Past is why deck did not send the last call of the turn: it was past a limit of the model in deck's settings.
	Past string
}

// Held is what of the outcome holds the provider and not only the turn: a usage limit or a lost login, else Done.
func (r Result) Held() Outcome {
	if r.Outcome == Limited || r.Outcome == Login {
		return r.Outcome
	}
	return Done
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
	// LocalBrowser is a shell that starts a local browser and listens on local ports.
	LocalBrowser = "local_browser"
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

// Refused marks a result by the status deck's proxy answered a call of a model with: a usage limit pauses until
// Retry-After, a refused key needs a login, a fault is a model that did not answer; false for any other status.
func Refused(res *Result, status int, retryAfter string, now time.Time) bool {
	switch fault.OfStatus(status, true) {
	case fault.ModelLimit:
		res.Outcome = Limited
		if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
			res.ResetsAt = now.Add(time.Duration(s) * time.Second).Unix()
		} else if t, err := http.ParseTime(retryAfter); err == nil {
			res.ResetsAt = t.Unix()
		}
	case fault.ModelLogin:
		res.Outcome = Login
	case fault.ModelUnavailable:
		res.Outcome = Unavailable
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
	// Outcome is Limited or Login when the check hit a usage limit or found no valid login.
	Outcome Outcome
	// ModelFailed is set when Err is the model not doing the steps of the check, not a fault of the adapter.
	ModelFailed bool
	// Slow is set when the model ran out of time rather than failed a step.
	Slow bool
	// Speed is the output tokens per second of the model over the check, zero when not measured.
	Speed float64
}

// Subscribed is a provider that runs on the owner's subscription: what its calls would cost is only an estimate.
type Subscribed interface {
	OnSubscription()
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

// Installed is a provider whose program may be missing from the machine.
type Installed interface {
	Installed() bool
}

// SelfChecker is a provider whose adapter deck checks at start, on a button and on a broken turn.
type SelfChecker interface {
	SelfCheck(ctx context.Context) Check
}

// SignIn is a CLI the owner signs in to from deck: Login is its program of a sign-in by a link, which prints the
// link and a code, or waits for the code the page of the link gives.
type SignIn interface {
	Login() (*exec.Cmd, error)
}

// CallLimiter is a provider whose calls of gateway tools time out: a wait for the owner ends before that and the
// turn stops, so the answer comes by resume.
type CallLimiter interface {
	CallLimit() time.Duration
}

// Terminal is a provider whose session the owner can go on with in a terminal: the command, in the folder of the turn.
type Terminal interface {
	Terminal(session string) (string, error)
}

// Spent adds up what calls cost: the paid ones, nil when one has no price or there is none, and apart what those
// on a subscription would cost, nil when there is none with a price.
func Spent[T any](calls []T, of func(T) (cost sql.NullFloat64, subscription bool)) (paid, subscribed *float64) {
	sum, sub, priced, n, m := 0.0, 0.0, true, 0, 0
	for _, c := range calls {
		cost, s := of(c)
		switch {
		case s && cost.Valid:
			sub, m = sub+cost.Float64, m+1
		case !s:
			sum, priced, n = sum+cost.Float64, priced && cost.Valid, n+1
		}
	}
	if n > 0 && priced {
		paid = &sum
	}
	if m > 0 {
		subscribed = &sub
	}
	return paid, subscribed
}

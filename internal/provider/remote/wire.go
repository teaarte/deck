// Package remote runs the turns of agents on a node: deck's side is a provider that hands each turn to the node over
// its connection, the node's side runs the same adapter there, in its own box and home, on its own sign-in.
package remote

import (
	"encoding/json"
	"errors"
	"time"

	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/node"
	"deck2/internal/provider"
)

// Paths of the calls deck makes of a node about its agents.
const (
	turnPath   = node.AgentPath + "turn"
	filesPath  = node.AgentPath + "files"
	gonePath   = node.AgentPath + "gone"
	checkPath  = node.AgentPath + "check"
	loginPath  = node.AgentPath + "login"
	scriptPath = node.AgentPath + "script"
)

// folderHeader marks a refusal of a node of a folder its owner did not allow.
const folderHeader = "Deck-Node-Folder"

// Spec is the provider a node builds for a turn: an agent, the model of deck's settings it runs and its effort, and
// for an ACP agent what deck's settings give it.
type Spec struct {
	Agent  string `json:"agent"`
	Name   string `json:"name"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// ACP is an agent of the Agent Client Protocol, which runs a model deck proxies.
	ACP          bool          `json:"acp,omitempty"`
	NoCloud      bool          `json:"no_cloud,omitempty"`
	CallLimit    time.Duration `json:"call_limit,omitempty"`
	CheckCeiling time.Duration `json:"check_ceiling,omitempty"`
}

// request is a turn as deck hands it to a node: its folders by their paths under deck's folder, or Folder, the folder
// of its project on the node's machine it works in, and Read, those it reads there.
type request struct {
	Project      int64             `json:"project"`
	Cwd          string            `json:"cwd,omitempty"`
	Folder       string            `json:"folder,omitempty"`
	Read         []string          `json:"read,omitempty"`
	Repo         bool              `json:"repo,omitempty"`
	Prompt       string            `json:"prompt"`
	ResultSchema json.RawMessage   `json:"result_schema,omitempty"`
	Skills       map[string]string `json:"skills,omitempty"`
	// MCP is the path of deck's MCP server of the turn on its side.
	MCP           string   `json:"mcp"`
	Dirs          []string `json:"dirs,omitempty"`
	Given         []string `json:"given,omitempty"`
	Data          string   `json:"data,omitempty"`
	Hosts         []string `json:"hosts,omitempty"`
	GPU           bool     `json:"gpu,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	PermissionMCP string   `json:"permission_mcp,omitempty"`
	SessionID     string   `json:"session_id"`
	Resume        bool     `json:"resume,omitempty"`
	Live          bool     `json:"live,omitempty"`
}

// start begins a turn: the provider, deck's side of the turn by its token, deck's folder the prompt names, the turn.
type start struct {
	Spec    Spec    `json:"spec"`
	Side    string  `json:"side"`
	Root    string  `json:"root"`
	Request request `json:"request"`
}

// frame is one line of a turn: deck sends the owner's live messages, the node the events, usage, whether it took a
// message, and at last how the turn ended.
type frame struct {
	Live  *string         `json:"live,omitempty"`
	Event *provider.Event `json:"event,omitempty"`
	Usage *provider.Usage `json:"usage,omitempty"`
	Taken *bool           `json:"taken,omitempty"`
	Done  *ended          `json:"done,omitempty"`
}

// ended is how a turn on a node ended: its result and the error of its run, as a failure of deck's or in words.
type ended struct {
	Result provider.Result  `json:"result"`
	Error  string           `json:"error,omitempty"`
	Fault  *fault.F         `json:"fault,omitempty"`
	Killed *provider.Killed `json:"killed,omitempty"`
}

// errorOf is the error of a run as a node tells it.
func errorOf(err error) ended {
	var e ended
	var f *fault.F
	var k *provider.Killed
	switch {
	case err == nil:
	case errors.As(err, &k):
		e.Killed = k
	case errors.As(err, &f):
		e.Fault = f
	default:
		e.Error = err.Error()
	}
	return e
}

// err is the error of a run a node told.
func (e ended) err() error {
	switch {
	case e.Killed != nil:
		return e.Killed
	case e.Fault != nil:
		return e.Fault
	case e.Error != "":
		return errors.New(e.Error)
	}
	return nil
}

// scriptCall is a script deck hands a node: its run folder by its path under deck's folder, the folders of its
// project it reads on the node's machine, and deck's side of it by its token.
type scriptCall struct {
	Project int64           `json:"project"`
	Script  manifest.Script `json:"script"`
	Source  string          `json:"source"`
	Draft   bool            `json:"draft,omitempty"`
	Dir     string          `json:"dir"`
	Stdin   []byte          `json:"stdin,omitempty"`
	Hosts   []string        `json:"hosts,omitempty"`
	GPU     bool            `json:"gpu,omitempty"`
	Folders []string        `json:"folders,omitempty"`
	Read    []string        `json:"read,omitempty"`
	Side    string          `json:"side"`
	Session string          `json:"session"`
}

// scriptFrame is one line of a script on a node: a line of its stderr, and at last how it ended, its stdout or its
// failure.
type scriptFrame struct {
	Line *string     `json:"line,omitempty"`
	Done *scriptDone `json:"done,omitempty"`
}

type scriptDone struct {
	Out   []byte   `json:"out,omitempty"`
	Error string   `json:"error,omitempty"`
	Fault *fault.F `json:"fault,omitempty"`
}

// checkCall asks a node for the self-check of an agent, an ACP agent on deck's side of the check.
type checkCall struct {
	Spec Spec   `json:"spec"`
	Side string `json:"side"`
}

// check is a self-check as a node tells it.
type check struct {
	Version     string  `json:"version,omitempty"`
	Error       string  `json:"error,omitempty"`
	Held        string  `json:"held,omitempty"`
	ModelFailed bool    `json:"model_failed,omitempty"`
	Slow        bool    `json:"slow,omitempty"`
	Speed       float64 `json:"speed,omitempty"`
}

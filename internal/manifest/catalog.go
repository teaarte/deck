package manifest

import (
	"deck2/internal/egress"
	"fmt"
	"slices"
	"strings"
)

// The rights of deck's catalog by name: all a module may ask the owner for beyond its own records, documents and files.
const (
	Network = "network"
	Login   = "login"
	Data    = "data"
	Files   = "files"
	Money   = "money"
	Send    = "send"
	Modules = "modules"
)

// Whom a network right opens to: the programs of a module, deck's tools for it among them, or the shells of its agents.
const (
	Programs = "programs"
	Agents   = "agent"
)

// GPU is the subject of files that is the graphics card.
const GPU = "gpu"

// Right is a row of the catalog: its name, what it lets a module do with %s for its subjects, whether it is a way out
// rather than something of value, and on which subjects an answer of always to it takes a fresh passkey.
type Right struct {
	Name string
	Says string
	Exit bool
	Key  Key
}

// Key is on which subjects of a right an answer of always takes the owner's fresh passkey.
type Key string

const (
	// KeyNever is no subject: the right stays near the module.
	KeyNever Key = ""
	// KeyAlways is every subject.
	KeyAlways Key = "always"
	// KeyWide is the whole internet, the shell of an agent, and the data of others: not a module's own services, nor
	// the records deck keeps of models and runs.
	KeyWide Key = "wide"
)

// Catalog is the closed list of rights; an eighth is the owner's decision and a row here, not a field of a manifest.
var Catalog = []Right{
	{Network, "its programs reach %s and may send its data there", true, KeyWide},
	{Login, "its agents work on your subscription login: %s", false, KeyAlways},
	{Data, "it reads your data: %s", false, KeyWide},
	{Files, "it uses on your machine: %s", false, KeyNever},
	{Money, "it spends money through %s", true, KeyAlways},
	{Send, "it acts in your name: %s", true, KeyAlways},
	{Modules, "it calls other modules: %s", true, KeyAlways},
}

// agentSays is what a network right of the shells of agents lets a module do; localSays what one on the local network
// of deck's machine lets its programs do.
const (
	agentSays = "its agents reach %s on their own, so its data and folders may go there"
	localSays = "its programs reach %s on your local network and may send its data there"
)

// RightOf is the row of the catalog of a right; false for a name outside it.
func RightOf(name string) (Right, bool) {
	i := slices.IndexFunc(Catalog, func(r Right) bool { return r.Name == name })
	if i < 0 {
		return Right{}, false
	}
	return Catalog[i], true
}

// Declared is a right a module may ask for: the right, its subjects, why in the module's words, and whom of a network.
type Declared struct {
	Right string   `json:"right"`
	What  []string `json:"what"`
	Why   string   `json:"why"`
	Who   string   `json:"who,omitempty"`
}

// Whom is whom a declared right opens to: programs or agents for a network, nobody else's for the rest.
func (d Declared) Whom() string { return whom(d.Right, d.Who) }

// WhomOf is whom a right opens to as grants keep it: programs or agents for a network, empty for the rest.
func WhomOf(right, who string) string { return whom(right, who) }

func whom(right, who string) string {
	if right != Network {
		return ""
	}
	if who == "" {
		return Programs
	}
	return who
}

// Says is what a right lets a module do on its subjects, in deck's words of the catalog translated by tr.
func Says(tr func(string) string, right, who string, subjects []string) string {
	r, ok := RightOf(right)
	text := r.Says
	switch {
	case whom(right, who) == Agents:
		text = agentSays
	case !ok:
		return right + ": " + strings.Join(subjects, ", ")
	case right == Network && len(subjects) > 0 && !slices.ContainsFunc(subjects, func(s string) bool { return !egress.Local(s) }):
		text = localSays
	}
	return fmt.Sprintf(tr(text), strings.Join(subjects, ", "))
}

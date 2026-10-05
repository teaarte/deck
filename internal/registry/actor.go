package registry

import (
	"fmt"
	"time"
)

const (
	Owner   = "owner"
	Master  = "master"
	Agent   = "agent"
	Trigger = "trigger"
	// Token is a caller with an automation token: it calls what the master may, limited to its commands.
	Token = "token"
)

// Actor is who invokes a command; agents and triggers act inside one project.
type Actor struct {
	Kind      string
	Workspace int64
	ProjectID int64
	// ProcessID is the calling agent's process, or for a trigger the process whose finish fired it.
	ProcessID int64
	TriggerID int64
	// Confirmed marks a call of a trigger the owner asked for or said yes to: its schedule checks do not apply.
	Confirmed bool
	// Token is the automation token of a caller of kind Token.
	Token int64
	// Commands narrow what a token or an agent whose command lists its calls may call.
	Commands []string
	// Trial marks a trial run: every call that is not internal is declined.
	Trial bool
	// CallLimit is how long a tool call of the calling agent may last; zero when it has no such limit.
	CallLimit time.Duration
	// cleared is set by the holder of the Clearance, the gateway, once a call that is not internal passed its
	// class rules.
	cleared bool
}

// Cleared says the gateway let a call that is not internal through.
func (a Actor) Cleared() bool { return a.cleared }

// Clearance lets its one holder, the gateway, clear an actor for calls that are not internal.
type Clearance struct{ r *Registry }

// Clear is the actor cleared for the call it makes now.
func (c Clearance) Clear(a Actor) Actor {
	if c.r == nil {
		panic("registry: a clearance the registry did not issue")
	}
	a.cleared = true
	return a
}

// Clearance issues the one clearance of the registry; a second ask panics, so only the gateway holds it.
func (r *Registry) Clearance() Clearance {
	if r.issued {
		panic("registry: the clearance is already issued")
	}
	r.issued = true
	return Clearance{r: r}
}

// Confined is an actor that stays inside its own project: an agent or a trigger.
func (a Actor) Confined() bool { return a.Kind == Agent || a.Kind == Trigger }

// OwnerSide is the owner or the master, who work across the projects of the workspace.
func (a Actor) OwnerSide() bool { return a.Kind == Owner || a.Kind == Master || a.Kind == Token }

// As is the kind of caller the actor counts as in the callers of a command: a token as the master.
func (a Actor) As() string {
	if a.Kind == Token {
		return Master
	}
	return a.Kind
}

// Within reports whether the actor may touch a project.
func (a Actor) Within(projectID int64) bool { return !a.Confined() || projectID == a.ProjectID }

// Author is the actor as recorded in document revisions.
func (a Actor) Author() string {
	if a.Kind == Token {
		return fmt.Sprintf("token:%d", a.Token)
	}
	if a.Kind == Agent && a.ProcessID != 0 {
		return fmt.Sprintf("agent:%d", a.ProcessID)
	}
	return a.Kind
}

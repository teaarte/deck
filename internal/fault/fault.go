// Package fault is the one form of a failure of a process: a type of a closed set, whose it is, a short text for
// the owner and the details apart.
package fault

import (
	"errors"
	"net/http"
	"slices"
	"strings"
)

// The types of a failure; the set is closed, the particulars go into the text and the details.
const (
	ModelLimit       = "model.limit"
	ModelUnavailable = "model.unavailable"
	ModelLogin       = "model.login"
	DeckTools        = "deck.tools"
	DeckRestart      = "deck.restart"
	DeckInternal     = "deck.internal"
	// DeckStopped is a process deck stopped itself: what it worked for is gone or another took its work.
	DeckStopped    = "deck.stopped"
	Timeout        = "timeout"
	ScriptFailed   = "script.failed"
	ResultInvalid  = "result.invalid"
	TaskFailed     = "task.failed"
	InputInvalid   = "input.invalid"
	ProgramMissing = "program.missing"
	RightsDenied   = "rights.denied"
	Network        = "network"
	Stopped        = "stopped"
)

// Whose a failure is.
const (
	OfModel       = "model"
	OfDeck        = "deck"
	OfModule      = "module"
	OfEnvironment = "environment"
	OfOwner       = "owner"
)

var whose = map[string]string{
	ModelLimit: OfModel, ModelUnavailable: OfModel, ModelLogin: OfModel,
	DeckTools: OfDeck, DeckRestart: OfDeck, DeckInternal: OfDeck, DeckStopped: OfDeck,
	Timeout: OfModule, ScriptFailed: OfModule, ResultInvalid: OfModule, TaskFailed: OfModule, InputInvalid: OfModule,
	ProgramMissing: OfEnvironment, Network: OfEnvironment,
	RightsDenied: OfOwner, Stopped: OfOwner,
}

// Whose is whose a failure of type t is; empty for no failure.
func Whose(t string) string { return whose[t] }

// ByScript are the types a script may give its own failure; any other becomes script.failed. One that gives none
// takes the type of deck's error answer to its last call, by the status, when that type is of this set.
var ByScript = []string{ScriptFailed, InputInvalid, ProgramMissing, RightsDenied, Network, ModelLimit, ModelUnavailable}

// F is a failure: its type, the text for the owner and the details (stderr, codes, the error of a program).
type F struct {
	Type    string
	Text    string
	Details string
}

func (f *F) Error() string {
	if f.Details == "" {
		return f.Text
	}
	return f.Text + ": " + f.Details
}

// New is a failure of type t.
func New(t, text string) *F { return &F{Type: t, Text: text} }

// With is a failure of type t with its details.
func With(t, text, details string) *F { return &F{Type: t, Text: text, Details: details} }

// Of is the failure an error carries, or deck's own one with its text.
func Of(err error) *F {
	if err == nil {
		return nil
	}
	var f *F
	if errors.As(err, &f) {
		return f
	}
	return &F{Type: DeckInternal, Text: err.Error()}
}

// Wrap keeps the failure of err under a prefix of what deck was doing.
func Wrap(prefix string, err error) *F {
	f := *Of(err)
	f.Text = prefix + ": " + f.Text
	return &f
}

// ModelStatus is the status deck answers a call with for what a model API answered: 401 for a refused key, 502 for
// a fault of the service, any other as it is.
func ModelStatus(status int) int {
	switch {
	case status == http.StatusForbidden:
		return http.StatusUnauthorized
	case status >= 500:
		return http.StatusBadGateway
	}
	return status
}

// OfStatus is the type of a failure of a call by the status deck answered it with; only a call of a model meets a
// model's limit, refused key or fault, any other one the network's; a 500 is deck's own.
func OfStatus(status int, model bool) string {
	switch {
	case !model && (status == http.StatusTooManyRequests || status > 500):
		return Network
	case status == http.StatusTooManyRequests:
		return ModelLimit
	case status > 500:
		return ModelUnavailable
	case model && status == http.StatusUnauthorized:
		return ModelLogin
	case status == http.StatusForbidden:
		return RightsDenied
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity || status == http.StatusNotFound:
		return InputInvalid
	}
	return DeckInternal
}

// FromScript is the type a script gave, kept only when a script may give it.
func FromScript(t string) string {
	t = strings.TrimSpace(t)
	if slices.Contains(ByScript, t) {
		return t
	}
	return ScriptFailed
}

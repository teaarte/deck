// Package fault is the one form of a failure of a process: a type of a closed set, whose it is, a short text for
// the owner and the details apart.
package fault

import (
	"net/http"
	"slices"
	"strings"
)

// The types of a failure; the set is closed, the particulars go into the text and the details.
const (
	ModelLimit       = "model.limit"
	ModelUnavailable = "model.unavailable"
	ModelLogin       = "model.login"
	// AgentLoop is an agent that made the same call of a tool with the same answer again and again.
	AgentLoop = "agent.loop"
	// AgentLeft is an agent whose turn ended without a result while the processes it started still worked.
	AgentLeft    = "agent.left"
	DeckTools    = "deck.tools"
	DeckRestart  = "deck.restart"
	DeckInternal = "deck.internal"
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
	// Killed is a program of a process a signal from outside deck ended.
	Killed  = "killed"
	Stopped = "stopped"
)

// The kinds of a call refused at its entry, by who calls, what or the input; the text goes verbatim to the owner and
// the agent. Only the constructors of registry make them.
const (
	CallRefused  = "call.refused"
	CallInvalid  = "call.invalid"
	CallNotFound = "call.not_found"
	// CallConflict is a call made on a state that changed or ended since the caller read it.
	CallConflict = "call.conflict"
	// CallGone is a call on what is over already: the caller reads it again rather than tells it.
	CallGone = "call.gone"
	// CallUnsigned is a call from no one deck knows.
	CallUnsigned = "call.unsigned"
	// CallLimited is a call from an address that used up its tries for a while.
	CallLimited = "call.limited"
	// CallUnkeyed is a call of the owner that takes a passkey touched lately, from a session that has none.
	CallUnkeyed = "call.unkeyed"
)

// refusals are the HTTP statuses of the kinds of a refusal.
var refusals = map[string]int{
	CallRefused: http.StatusForbidden, CallInvalid: http.StatusUnprocessableEntity, CallNotFound: http.StatusNotFound,
	CallConflict: http.StatusConflict, CallGone: http.StatusGone, CallUnsigned: http.StatusUnauthorized,
	CallLimited: http.StatusTooManyRequests, CallUnkeyed: http.StatusPreconditionRequired,
}

// Refusal is the refusal at its entry err carries anywhere in its chain, nil for none.
func Refusal(err error) *F { return find(err, true) }

// Failure is the first failure err carries in its chain that is not a refusal, nil for none.
func Failure(err error) *F { return find(err, false) }

// find is the first *F in the chain of err that is a refusal or is not, depth first as errors.As goes.
func find(err error, refused bool) *F {
	if f, ok := err.(*F); ok && (refusals[f.Type] != 0) == refused {
		return f
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return find(u.Unwrap(), refused)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if f := find(e, refused); f != nil {
				return f
			}
		}
	}
	return nil
}

// Whose a failure is.
const (
	OfModel       = "model"
	OfDeck        = "deck"
	OfModule      = "module"
	OfEnvironment = "environment"
	OfOwner       = "owner"
)

var whose = map[string]string{
	ModelLimit: OfModel, ModelUnavailable: OfModel, ModelLogin: OfModel, AgentLoop: OfModel, AgentLeft: OfModel,
	DeckTools: OfDeck, DeckRestart: OfDeck, DeckInternal: OfDeck, DeckStopped: OfDeck,
	Timeout: OfModule, ScriptFailed: OfModule, ResultInvalid: OfModule, TaskFailed: OfModule, InputInvalid: OfModule,
	ProgramMissing: OfEnvironment, Network: OfEnvironment, Killed: OfEnvironment,
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

// Of is the failure an error carries, or deck's own one with its text; a refusal is deck's own too.
func Of(err error) *F {
	if err == nil {
		return nil
	}
	if f := Failure(err); f != nil {
		return f
	}
	return &F{Type: DeckInternal, Text: err.Error()}
}

// OfCall is the failure of a call deck made: its own, a refusal's by its status, any other deck's own.
func OfCall(err error) *F {
	if f := Failure(err); f != nil {
		return &F{Type: f.Type, Text: err.Error()}
	}
	if r := Refusal(err); r != nil {
		return &F{Type: OfStatus(refusals[r.Type], false), Text: err.Error()}
	}
	return &F{Type: DeckInternal, Text: err.Error()}
}

// statuses are the HTTP statuses of failures by type; any other is 500.
var statuses = map[string]int{
	ModelLimit: http.StatusTooManyRequests, ModelUnavailable: http.StatusBadGateway, ModelLogin: http.StatusUnauthorized,
	Network: http.StatusBadGateway, RightsDenied: http.StatusForbidden, InputInvalid: http.StatusUnprocessableEntity,
}

// Status is the HTTP status deck answers a failed call with: a refusal's by its kind, a failure's by its type, else
// 500.
func Status(err error) int {
	if r := Refusal(err); r != nil {
		return refusals[r.Type]
	}
	if f := Failure(err); f != nil && statuses[f.Type] != 0 {
		return statuses[f.Type]
	}
	return http.StatusInternalServerError
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

// limitWords are words of an answer that names a limit per time rather than the size of what a model takes;
// minuteWords those of a limit per minute, which one request is past by itself.
var (
	limitWords  = []string{"rate limit", "rate_limit", "per minute", "tpm"}
	minuteWords = []string{"per minute", "tpm"}
)

// ModelAnswer reads a 413 of a model API by its words: past a limit per minute is the request's own, PastMinute;
// past another rate is 429, a limit the model waits out; else a request too big for the model.
func ModelAnswer(status int, body []byte) int {
	if status != http.StatusRequestEntityTooLarge {
		return status
	}
	text := strings.ToLower(string(body))
	switch {
	case slices.ContainsFunc(minuteWords, func(w string) bool { return strings.Contains(text, w) }):
		return PastMinute
	case slices.ContainsFunc(limitWords, func(w string) bool { return strings.Contains(text, w) }):
		return http.StatusTooManyRequests
	}
	return status
}

// PastMinute is what ModelAnswer reads a request past what the model takes a minute as.
const PastMinute = -413

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
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity || status == http.StatusNotFound ||
		status == http.StatusRequestEntityTooLarge:
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

// said is a failure by its type in the owner's words, a phrase of deck to translate.
var said = map[string]string{
	ModelLimit: "the model's usage limit", ModelUnavailable: "the model did not answer", ModelLogin: "the model wants a login",
	AgentLoop: "the agent went round in circles", AgentLeft: "the agent did not wait for the work it started", DeckTools: "deck's tools failed", DeckRestart: "deck restarted",
	DeckInternal: "an error of deck", DeckStopped: "deck stopped it", Timeout: "it ran out of time", Network: "the network failed",
	RightsDenied: "a right was refused", Stopped: "you stopped it", TaskFailed: "the agent reported an error",
	Killed: "a signal from outside deck killed it",
}

// Said is a failure of type t in words, a phrase of deck; empty for a type the text says better.
func Said(t string) string { return said[t] }

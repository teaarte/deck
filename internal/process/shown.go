package process

import (
	"cmp"
	"fmt"
	"slices"

	"deck2/internal/store/db"
)

// Shown is the state of a process as the owner reads it: a word in their language, its tone, and whether the
// process waits for the owner or its provider rather than works.
type Shown struct {
	Word  string `json:"word"`
	Tone  string `json:"tone,omitempty"`
	Waits bool   `json:"waits,omitempty"`
}

// held are the reasons an open process waits for the owner or its provider.
var held = []string{WaitAnswer, WaitSignIn, WaitLimit, WaitProvider}

// Show says the state of a process: question is what it waits for the owner to answer, long that it works more
// than twice as long as usual; tr puts a phrase, with %s for its detail, in the owner's language.
func Show(p db.Process, question string, long bool, tr func(string) string) Shown {
	say := func(phrase string, detail ...any) string { return fmt.Sprintf(tr(phrase), detail...) }
	s := Shown{Waits: !p.FinishedAt.Valid && p.State == StateWaiting && slices.Contains(held, p.WaitReason)}
	if s.Waits {
		s.Tone = "warn"
	}
	switch {
	case p.State == StateDone && p.NotBySchema == 1:
		s.Word, s.Tone = say("the result did not match its schema"), "error"
	case p.State == StateDone:
		s.Word, s.Tone = say("done"), "ok"
	case p.State == StateError:
		s.Word, s.Tone = say("failed"), "error"
	case p.State == StateInterrupted:
		s.Word, s.Tone = say("cut by a restart of deck"), "error"
	case p.State == StateStopped:
		s.Word, s.Tone = say("stopped"), "muted"
	case question != "":
		s.Word = say("waits for your answer: %s", question)
	case Waits(p, WaitAnswer):
		s.Word = say("waits for you")
	case Waits(p, WaitSignIn):
		s.Word = say("waits for a login")
	case Waits(p, WaitLimit):
		s.Word = say("paused on a usage limit")
	case Waits(p, WaitProvider):
		s.Word = say("waits for %s to work again", cmp.Or(p.Provider, p.Kind))
	case Waits(p, WaitSlot):
		s.Word = say("waits for a slot")
	case Waits(p, WaitFolder):
		s.Word = say("waits for folder %s", p.Folder)
	case Waits(p, WaitRun):
		s.Word = say("waits for the run before it")
	case p.SilentSinceMs.Valid:
		s.Word = say("silent")
	case p.Kind == KindScript:
		s.Word = say("a script runs")
	default:
		s.Word = say("runs on %s", cmp.Or(p.Provider, p.Kind))
	}
	if long {
		s.Word, s.Tone = say("longer than usual: %s", s.Word), "warn"
	}
	return s
}

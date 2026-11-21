package registry

import (
	"fmt"
	"strings"

	"deck2/internal/fault"
)

// refused is a call refused at its entry, a failure of a kind of fault, with deck's prefix before its text; the text
// goes verbatim to the owner and the agent.
func refused(kind string, format string, a ...any) error {
	text := fmt.Sprintf(format, a...)
	if !strings.HasPrefix(text, "deck: ") {
		text = "deck: " + text
	}
	return fault.New(kind, text)
}

func Refuse(format string, a ...any) error { return refused(fault.CallRefused, format, a...) }

func Invalid(format string, a ...any) error {
	return refused(fault.CallInvalid, format, a...)
}

func NotFound(format string, a ...any) error { return refused(fault.CallNotFound, format, a...) }

// Conflict is a call made on a state that changed or ended since the caller read it.
func Conflict(format string, a ...any) error { return refused(fault.CallConflict, format, a...) }

// Gone is a call on what is over already, such as an ask answered: the caller reads it again rather than tells it.
func Gone(format string, a ...any) error { return refused(fault.CallGone, format, a...) }

// Unsigned is a call from no one deck knows: no token of deck and no signed-in device.
func Unsigned(format string, a ...any) error { return refused(fault.CallUnsigned, format, a...) }

// Limited is a call from an address that used up its tries for a while.
func Limited(format string, a ...any) error { return refused(fault.CallLimited, format, a...) }

// Unkeyed is a call of the owner that takes a passkey touched within FreshKey, from a session that has none: the
// client asks the owner to touch it and calls again.
func Unkeyed(format string, a ...any) error {
	return refused(fault.CallUnkeyed, format, a...)
}

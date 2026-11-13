package registry

import (
	"fmt"
	"net/http"
	"strings"
)

// Error is a call refused at its entry: who calls, what or the input; the text goes verbatim to the owner and the
// agent. Any other failure of a call is a fault.F; fault.Status gives the HTTP status of either.
type Error struct {
	status int
	text   string
}

func (e *Error) Error() string { return e.text }

// Refused is the HTTP status the call is refused with.
func (e *Error) Refused() int { return e.status }

// refused is a refusal with deck's prefix before its text.
func refused(status int, format string, a ...any) error {
	text := fmt.Sprintf(format, a...)
	if !strings.HasPrefix(text, "deck: ") {
		text = "deck: " + text
	}
	return &Error{status, text}
}

func Refuse(format string, a ...any) error { return refused(http.StatusForbidden, format, a...) }

func Invalid(format string, a ...any) error {
	return refused(http.StatusUnprocessableEntity, format, a...)
}

func NotFound(format string, a ...any) error { return refused(http.StatusNotFound, format, a...) }

// Conflict is a call made on a state that changed or ended since the caller read it.
func Conflict(format string, a ...any) error { return refused(http.StatusConflict, format, a...) }

// Unsigned is a call from no one deck knows: no token of deck and no signed-in device.
func Unsigned(format string, a ...any) error { return refused(http.StatusUnauthorized, format, a...) }

// Limited is a call from an address that used up its tries for a while.
func Limited(format string, a ...any) error { return refused(http.StatusTooManyRequests, format, a...) }

// Unkeyed is a call of the owner that takes a passkey touched within FreshKey, from a session that has none: the
// client asks the owner to touch it and calls again.
func Unkeyed(format string, a ...any) error {
	return refused(http.StatusPreconditionRequired, format, a...)
}

package registry

import (
	"errors"
	"fmt"
	"net/http"
)

// Error carries the text shown verbatim to the owner and the agent, and an HTTP status.
type Error struct {
	Status int
	Text   string
}

func (e *Error) Error() string { return e.Text }

func Refuse(format string, a ...any) error {
	return &Error{http.StatusForbidden, fmt.Sprintf(format, a...)}
}

func Invalid(format string, a ...any) error {
	return &Error{http.StatusUnprocessableEntity, fmt.Sprintf(format, a...)}
}

func NotFound(format string, a ...any) error {
	return &Error{http.StatusNotFound, fmt.Sprintf(format, a...)}
}

// Conflict is a call made on a state that changed or ended since the caller read it.
func Conflict(format string, a ...any) error {
	return &Error{http.StatusConflict, fmt.Sprintf(format, a...)}
}

// Status maps an invocation error to an HTTP status.
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return http.StatusInternalServerError
}

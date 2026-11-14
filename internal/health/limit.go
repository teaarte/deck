package health

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// OfLimit is the kind of the rows of a usage limit wider than one model: every model of a CLI's connection, or the
// free models of an API connection.
const OfLimit = "limit"

// Limit is the subject of the limit of every model of a connection, the login of a CLI.
func Limit(connection string) string { return OfLimit + ":" + connection }

// FreeLimit is the subject of the limit of the free models of a connection, the free requests of its account.
func FreeLimit(connection string) string { return Limit(connection) + ":free" }

// Pause writes a paused row of subject until a time, why being what the model answered.
func Pause(ctx context.Context, q *db.Queries, now time.Time, of Of, subject string, until time.Time, why string) error {
	_, err := Set(ctx, q, now, of, subject, Paused, PauseText(until, why))
	return err
}

// PauseText is the text of a paused row: when it ends and why.
func PauseText(until time.Time, why string) string {
	return fmt.Sprintf("usage limit reached; next try at %s: %s", store.Time(until), untrusted.Cut(strings.TrimSpace(why), 500))
}

var pauseEnd = regexp.MustCompile(`^usage limit reached; next try at (\S+):`)

// Until is when the pause of a paused row ends, zero for a row that is not paused or says no end.
func Until(h db.Health) time.Time {
	if h.Status != Paused {
		return time.Time{}
	}
	m := pauseEnd.FindStringSubmatch(h.Error)
	if m == nil {
		return time.Time{}
	}
	t, err := store.ParseTime(m[1])
	if err != nil {
		return time.Time{}
	}
	return t
}

// Ended says a paused row's end has come by now.
func Ended(h db.Health, now time.Time) bool {
	end := Until(h)
	return !end.IsZero() && !now.Before(end)
}

package harness

import (
	"deck2/internal/provider"
	"time"
)

func Limited(resetsAt time.Time) provider.Result {
	r := provider.Result{Outcome: provider.Limited, Text: "You've hit your limit"}
	if !resetsAt.IsZero() {
		r.ResetsAt = resetsAt.Unix()
	}
	return r
}

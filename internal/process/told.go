package process

import (
	"context"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// told names the answers, expiries and remarks a resume carries; they count as delivered once the provider
// has the turn.
type told struct {
	Asks    []int64 `json:"asks,omitempty"`
	Remarks []int64 `json:"remarks,omitempty"`
}

func (t told) mark(ctx context.Context, q *db.Queries) error {
	for _, id := range t.Asks {
		if _, err := q.DeliverAsk(ctx, id); err != nil {
			return err
		}
	}
	for _, id := range t.Remarks {
		if err := q.MarkRemarkDelivered(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (t told) add(more told) told {
	return told{Asks: append(t.Asks, more.Asks...), Remarks: append(t.Remarks, more.Remarks...)}
}

// remarksText is what the owner wrote to a process in the remarks named, for a fresh session that takes them.
func (r *Runner) remarksText(ctx context.Context, processID int64, ids []int64) string {
	remarks, _ := r.st.Q.ListUndeliveredRemarks(ctx, processID)
	var b strings.Builder
	for _, m := range remarks {
		if slices.Contains(ids, m.ID) {
			b.WriteString("\nThe owner wrote while you worked:\n" + strings.TrimSpace(m.Text) + "\n")
		}
	}
	return b.String()
}

// untold is what a process has not been told yet: decisions with what deck made of a call, and remarks.
type untold struct {
	asks    []db.ListUndeliveredAsksRow
	remarks []db.ProcessRemark
}

func (r *Runner) untold(ctx context.Context, processID int64) (untold, error) {
	asks, err := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if err != nil {
		return untold{}, err
	}
	remarks, err := r.st.Q.ListUndeliveredRemarks(ctx, processID)
	return untold{asks, remarks}, err
}

func (u untold) empty() bool { return len(u.asks) == 0 && len(u.remarks) == 0 }

func (u untold) ids() told {
	var t told
	for _, a := range u.asks {
		t.Asks = append(t.Asks, a.Ask.ID)
	}
	for _, m := range u.remarks {
		t.Remarks = append(t.Remarks, m.ID)
	}
	return t
}

// except leaves out what a resume already carries, and asks of a kind.
func (u untold) except(t told, kind string) untold {
	var out untold
	for _, a := range u.asks {
		if a.Ask.Kind != kind && !slices.Contains(t.Asks, a.Ask.ID) {
			out.asks = append(out.asks, a)
		}
	}
	for _, m := range u.remarks {
		if !slices.Contains(t.Remarks, m.ID) {
			out.remarks = append(out.remarks, m)
		}
	}
	return out
}

func (u untold) message() string {
	var b strings.Builder
	b.WriteString("deck: the owner has responded to what you were waiting for.\n")
	for _, m := range u.remarks {
		b.WriteString("- The owner wrote while you worked:\n" + strings.TrimSpace(m.Text) + "\n")
	}
	for _, row := range u.asks {
		a := row.Ask
		what := untrusted.Line(a.Text, 600)
		switch {
		case a.Kind == ask.KindLogin:
		case a.Kind == ask.KindQuestion && a.State == "answered":
			b.WriteString("- Question: " + untrusted.Line(a.Text, 1000) + "\n" + untrusted.Value("Answer", a.Answer, 500) + "\n")
		case a.Kind == ask.KindCall && a.State == "answered" && row.Result != "":
			b.WriteString("- Allowed: " + what + ". deck made this call for you; do not make it again yourself. " + outcomeText(row.Result) + "\n")
		case a.Kind == ask.KindCall && a.State == "answered":
			b.WriteString("- Rejected: " + what + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == ask.KindCall:
			b.WriteString("- Expired: " + what + ". The owner did not answer in time; the call was not made.\n")
		case a.Kind == ask.KindUnknown && a.Answer == ask.UnknownRetry:
			b.WriteString("- The owner had deck retry a call of unknown outcome. " + outcomeText(row.Result) + "\n")
		case a.Kind == ask.KindSignIn && a.State == "answered":
			b.WriteString("- The owner signed in: " + what + " Go on with the site.\n")
		case a.Kind == ask.KindUnknown:
			b.WriteString("- " + what + " The owner counts it as done; do not make it again.\n")
		case a.Kind == ask.KindConfirm && a.State == "answered" && a.Answer == "allow":
			b.WriteString("- Allowed: " + what + ". Make exactly the same call again; it will go through.\n")
		case a.Kind == ask.KindConfirm && a.State == "answered":
			b.WriteString("- Rejected: " + what + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == ask.KindConfirm:
			b.WriteString("- Expired: " + what + ". The call was not made.\n")
		default:
			b.WriteString("- Expired without an answer: " + what + "\n")
		}
	}
	b.WriteString("Continue the task.")
	return b.String()
}

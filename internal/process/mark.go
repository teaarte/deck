package process

import (
	"context"
	"log/slog"
	"strings"

	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// know keeps what a process knows as it starts: the untrusted text a box with the network brings, whose reads deck
// does not see, and whether it holds something of value: a subscription login, or the data of its module.
func (r *Runner) know(ctx context.Context, id int64, hosts []string, valued bool) {
	err := r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if len(hosts) > 0 {
			from := "the network of its box: " + untrusted.Cut(strings.Join(hosts, ", "), 300)
			if err := q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: from, ID: id}); err != nil {
				return err
			}
		}
		if !valued {
			return nil
		}
		return q.SetProcessValued(ctx, id)
	})
	if err != nil {
		slog.Error("what a process knows", "process", id, "err", err)
	}
}

// Read marks a process with the untrusted text it is given, as where it came from.
func (r *Runner) Read(ctx context.Context, id int64, from string) {
	err := r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: untrusted.Cut(from, 500), ID: id})
	})
	if err != nil {
		slog.Error("mark a process", "process", id, "err", err)
	}
}

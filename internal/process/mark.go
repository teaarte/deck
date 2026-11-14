package process

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Opens is what a running process holds open: the hosts its box reaches and the CLI whose login it runs on.
type Opens struct {
	Hosts []string `json:"hosts,omitempty"`
	Login string   `json:"login,omitempty"`
}

// know keeps what a process knows as it starts: what its box opens, the untrusted text a box with the network brings,
// and whether it holds something of value: a subscription login, or the data of its module.
func (r *Runner) know(ctx context.Context, id int64, hosts []string, login string, valued bool) {
	err := r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if len(hosts) > 0 || login != "" {
			b, _ := json.Marshal(Opens{Hosts: hosts, Login: login})
			if err := q.SetProcessOpens(ctx, db.SetProcessOpensParams{Opens: string(b), ID: id}); err != nil {
				return err
			}
		}
		if len(hosts) > 0 {
			from := fmt.Sprintf("the network of its box: %s", untrusted.Cut(strings.Join(hosts, ", "), 300))
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

// Source is a piece of what deck puts into a turn's prompt or input, by where it came from: the words of a process or
// the data of a module.
type Source struct {
	// Process is the process whose words it is, said by OfProcess of its id and what it read.
	Process   int64
	OfProcess string
	// Project is the module whose data it is, said by OfProject of its name and who wrote there.
	Project   int64
	OfProject string
	// Token is an automation token whose words it is, said by OfToken of its name.
	Token   int64
	OfToken string
}

// carried is where the untrusted text of the first of sources that holds some came from, empty for none.
func carried(ctx context.Context, q *db.Queries, sources ...Source) string {
	for _, s := range sources {
		if s.Process != 0 {
			if p, err := q.GetProcess(ctx, s.Process); err == nil && p.ReadUntrusted != "" {
				return fmt.Sprintf(s.OfProcess, p.ID, p.ReadUntrusted)
			}
		}
		if s.Project != 0 {
			if p, err := q.GetProject(ctx, s.Project); err == nil && p.Untrusted != "" {
				return fmt.Sprintf(s.OfProject, p.Name, p.Untrusted)
			}
		}
		if s.Token != 0 {
			if t, err := q.GetToken(ctx, s.Token); err == nil && t.Trusted == 0 {
				return fmt.Sprintf(s.OfToken, t.Name)
			}
		}
	}
	return ""
}

// Carries marks a process with the untrusted text the first of sources it is given holds.
func (r *Runner) Carries(ctx context.Context, id int64, sources ...Source) {
	if from := carried(ctx, r.st.Q, sources...); from != "" {
		r.Read(ctx, id, from)
	}
}

// carries marks a process within a transaction by what sources hold.
func carries(ctx context.Context, q *db.Queries, id int64, sources ...Source) error {
	from := carried(ctx, q, sources...)
	if from == "" {
		return nil
	}
	return q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: untrusted.Cut(from, 500), ID: id})
}

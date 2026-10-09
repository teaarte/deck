package asks

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Unheard hands the master an answer of the owner that its process did not act on: the process asked last, got
// the answer or not, and ended with nothing; the owner's feed says so.
type Unheard struct {
	process.NoHook
	S *Service
}

func (h Unheard) Closed(ctx context.Context, p db.Process) {
	if p.State != process.StateDone || p.Result != process.Empty || p.Kind != process.KindAgent {
		return
	}
	last, err := h.S.St.Q.LastAskOfProcess(ctx, store.ID(p.ID))
	if err != nil || last.Kind != ask.KindQuestion || last.State != ask.StateAnswered {
		return
	}
	h.S.unheard(ctx, p, last, "it ended without doing anything after it")
}

// unheard tells the master on its next turn an answer of the owner p did not act on, and the owner's feed that the
// master has it; the answer counts as delivered.
func (s *Service) unheard(ctx context.Context, p db.Process, a db.Ask, why string) {
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Builtin == 1 || p.Trial == 1 {
		return
	}
	mp, err := access.Master(ctx, s.St.Q, proj.WorkspaceID)
	if err != nil {
		return
	}
	who := fmt.Sprintf("process #%d (%s.%s)", p.ID, proj.Name, p.Command)
	answer := answered(a.Answer)
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		// A close heard again after a crash tells the master once.
		if n, err := q.TellUnheard(ctx, a.ID); err != nil || n == 0 {
			return err
		}
		if n, err := q.DeliverAsk(ctx, a.ID); err != nil || n == 0 && !a.DeliveredAt.Valid {
			return err
		}
		err := q.AddMasterNote(ctx, db.AddMasterNoteParams{CreatedAt: store.Time(s.Now()), WorkspaceID: proj.WorkspaceID, ProjectID: mp.ID,
			Text: fmt.Sprintf("the owner answered a question of %s and %s; question: %s; answer: %s",
				who, why, untrusted.Cut(a.Text, 500), untrusted.Cut(answer, 500))})
		if err != nil {
			return err
		}
		return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(s.Now()), ProjectID: proj.ID,
			Text: fmt.Sprintf("%s ended with nothing after your answer %q; the master has it on its next turn.", who, untrusted.Cut(answer, 200))})
	})
}

// answered is the answer to a question as text: the answer to each question of a JSON object, else as it is.
func answered(raw string) string {
	var byQuestion map[string]string
	if json.Unmarshal([]byte(raw), &byQuestion) != nil || len(byQuestion) == 0 {
		return raw
	}
	parts := []string{}
	for _, k := range slices.Sorted(maps.Keys(byQuestion)) {
		parts = append(parts, byQuestion[k])
	}
	return strings.Join(parts, "; ")
}

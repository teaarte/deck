package ask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/events"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	KindQuestion = "question"
	KindConfirm  = "confirm"
	// KindLogin asks the owner to log the provider in again and pick one of LoginAnswers.
	KindLogin = "login"
	// KindGrant waits for the owner to allow a frozen call that widens the rights of a project.
	KindGrant = "grant"
	// KindCall waits for the owner to allow a frozen gateway call that deck then makes itself.
	KindCall = "call"
	// KindUnknown is an external call whose outcome deck lost; the owner answers with one of UnknownAnswers.
	KindUnknown = "unknown"
	// KindSignIn asks the owner to sign in to a site or service; its payload names the site or the tool.
	KindSignIn = "sign_in"
	// KindLimit offers to continue a process stopped when its pause on a usage limit ran out.
	KindLimit = "limit"
	// KindSuggest is a draft of a card or a call that waits for the owner's yes; its payload holds the draft.
	KindSuggest = "suggest"
)

// LimitContinue is the one answer to a limit ask.
const LimitContinue = "continue"

const (
	LoginRetry      = "retry"
	LoginNewSession = "new_session"
)

var LoginAnswers = []string{LoginRetry, LoginNewSession}

const (
	UnknownRetry = "retry"
	UnknownDone  = "done"
)

var UnknownAnswers = []string{UnknownRetry, UnknownDone}

// answers are what a kind may be answered with, "deny:" taking a reason after it, and what to say to another
// answer; a kind not listed takes any text.
var answers = map[string]struct {
	ok  []string
	say string
}{
	KindLogin:   {LoginAnswers, "answer a login ask with retry or new_session"},
	KindLimit:   {[]string{LimitContinue}, "answer a stopped pause with continue"},
	KindUnknown: {UnknownAnswers, "answer an unknown outcome with retry or done"},
	KindCall:    {[]string{"allow", "allow_always", "deny", "deny:"}, "answer a confirmation with allow, allow_always, deny or deny:<reason>"},
}

// Check refuses an answer the kind does not take.
func Check(kind, answer string) error {
	a, ok := answers[kind]
	if !ok || slices.ContainsFunc(a.ok, func(o string) bool { return answer == o || strings.HasSuffix(o, ":") && strings.HasPrefix(answer, o) }) {
		return nil
	}
	return registry.Invalid("deck: %s", a.say)
}

// Refused reports a no to a call: deny, with or without a reason.
func Refused(answer string) bool { return answer == "deny" || strings.HasPrefix(answer, "deny:") }

// New stores an ask a process waits on in the caller's transaction; a zero expires is no deadline.
func New(ctx context.Context, q *db.Queries, processID int64, kind, text, payload string, expires time.Time) (db.Ask, error) {
	return q.CreateAsk(ctx, db.CreateAskParams{ProcessID: processID, Kind: kind, Text: text, Payload: payload, ExpiresAt: At(expires)})
}

// ForProject stores an ask of a project that no process, or the given one, waits on.
func ForProject(ctx context.Context, q *db.Queries, workspace, project, processID int64, kind, text, payload string, expires time.Time) (db.Ask, error) {
	return q.CreateProjectAsk(ctx, db.CreateProjectAskParams{ProcessID: sql.NullInt64{Int64: processID, Valid: processID != 0},
		WorkspaceID: workspace, ProjectID: store.ID(project), Kind: kind, Text: text, Payload: payload, ExpiresAt: At(expires)})
}

// Remark keeps a message of the owner for an open process; sql.ErrNoRows when the process has finished.
func Remark(ctx context.Context, q *db.Queries, processID int64, text, at string) (db.ProcessRemark, error) {
	return q.CreateRemark(ctx, db.CreateRemarkParams{ProcessID: processID, Text: text, CreatedAt: at})
}

var ErrNotOpen = errors.New("ask is not open")

// NotOpen is what a caller gets for an answer to an ask that is answered or expired already.
var NotOpen = registry.Conflict("deck: this ask is already answered or expired")

// Broker stores owner-facing asks and lets a live caller block until the owner answers. Waits end by the
// deck clock, checked at least every Poll, so a sleep of the machine does not stretch them.
type Broker struct {
	st   *store.Store
	bus  *events.Bus
	now  func() time.Time
	Poll time.Duration

	mu      sync.Mutex
	waiters map[int64]chan string
	late    func(ctx context.Context, a db.Ask)
}

func NewBroker(st *store.Store, bus *events.Bus, now func() time.Time) *Broker {
	return &Broker{st: st, bus: bus, now: now, Poll: time.Second, waiters: map[int64]chan string{}}
}

// OnLateAnswer is called when an answer arrives and nobody is waiting live (the process has already stopped).
func (b *Broker) OnLateAnswer(f func(ctx context.Context, a db.Ask)) { b.late = f }

// Create stores an ask; a zero expires means it never expires by itself.
func (b *Broker) Create(ctx context.Context, processID int64, kind, text string, payload any, expires time.Time) (db.Ask, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return db.Ask{}, err
	}
	return b.CreateRaw(ctx, processID, kind, text, string(raw), expires)
}

// CreateRaw stores an ask with a payload that is already JSON, so it can be matched byte for byte later.
func (b *Broker) CreateRaw(ctx context.Context, processID int64, kind, text, payload string, expires time.Time) (db.Ask, error) {
	a, err := store.TxGet(b.st, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return New(ctx, q, processID, kind, text, payload, expires)
	})
	if err != nil {
		return db.Ask{}, err
	}
	b.mu.Lock()
	b.waiters[a.ID] = make(chan string, 1)
	b.mu.Unlock()
	return a, nil
}

// Wait blocks until the ask is answered, the deck clock reaches until or ctx ends; ok is false without an answer.
func (b *Broker) Wait(ctx context.Context, id int64, until time.Time) (answer string, ok bool) {
	b.mu.Lock()
	ch := b.waiters[id]
	b.mu.Unlock()
	if ch == nil {
		return "", false
	}
	if a, err := b.st.Q.GetAsk(ctx, id); err == nil && a.State == "answered" {
		b.drop(id)
		return a.Answer, true
	}
wait:
	for left := until.Sub(b.now()); left > 0; left = until.Sub(b.now()) {
		t := time.NewTimer(min(left, b.Poll))
		select {
		case a := <-ch:
			t.Stop()
			b.drop(id)
			return a, true
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			break wait
		}
	}
	b.drop(id)
	select {
	case a := <-ch:
		return a, true
	default:
		return "", false
	}
}

// At is a stored deadline; the zero time is none.
func At(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: store.Time(t), Valid: true}
}

func (b *Broker) drop(id int64) {
	b.mu.Lock()
	delete(b.waiters, id)
	b.mu.Unlock()
}

func (b *Broker) Answer(ctx context.Context, id int64, answer string) error {
	n, err := store.TxGet(b.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.AnswerAsk(ctx, db.AnswerAskParams{Answer: answer, ID: id})
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotOpen
	}
	b.mu.Lock()
	ch := b.waiters[id]
	if ch != nil {
		ch <- answer
	}
	b.mu.Unlock()
	if ch != nil {
		return nil
	}
	if b.late != nil {
		a, err := b.st.Q.GetAsk(ctx, id)
		if err != nil {
			return err
		}
		go b.late(context.WithoutCancel(ctx), a)
	}
	return nil
}

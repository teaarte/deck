package process

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	StateRunning = "running"
	// StateWaiting is a process that waits: why, on what, until when and what its silence comes to are its wait.
	StateWaiting     = "waiting"
	StateDone        = "done"
	StateError       = "error"
	StateInterrupted = "interrupted"
	StateStopped     = "stopped"
)

// The reasons a process waits.
const (
	// WaitAnswer waits for the owner to answer an ask; WaitSignIn for the owner to log its provider in again.
	WaitAnswer = "answer"
	WaitSignIn = "sign_in"
	// WaitRight waits, before a turn starts, for the owner's answer to a right it needs, a login or a network.
	WaitRight = "right"
	// WaitLimit is a pause on a provider's usage limit until it resets; WaitProvider waits for a red provider.
	WaitLimit    = "limit"
	WaitProvider = "provider"
	// WaitSlot waits for one of the agent turns a workspace may run at once, WaitFolder for another agent process to
	// leave the project folder, WaitRun for the run before it of a command that runs one at a time.
	WaitSlot   = "slot"
	WaitFolder = "folder"
	WaitRun    = "run"
)

// What the silence of a wait comes to at its term: try again, go on without the answer, or fail with a type after
// SilenceFail. A wait without a term of its own has none.
const (
	SilenceRetry = "retry"
	SilenceGoOn  = "go_on"
	SilenceFail  = "fail:"
)

// Waits says p waits for reason.
func Waits(p db.Process, reason string) bool {
	return p.State == StateWaiting && p.WaitReason == reason
}

// Final are the states a process ends in; the client reads the same list from the catalog.
var Final = []string{StateDone, StateError, StateInterrupted, StateStopped}

// Ceiling is how long a command agent may work, ScriptCeiling a command script or tool, when the manifest sets
// no timeout.
const (
	Ceiling       = time.Hour
	ScriptCeiling = 10 * time.Minute
)

// The terms of the waits deck sets itself: for a red provider, for one whose agent did not get deck's tools, for a login.
const (
	providerTerm = 24 * time.Hour
	toolsTerm    = time.Hour
	signInTerm   = 7 * 24 * time.Hour
)

// MaxDepth is the longest chain of processes that start one another.
const MaxDepth = 8

// Empty is the result of a process that had nothing to do.
const Empty = "null"

const (
	KindAgent  = "agent"
	KindScript = "script"
	KindTool   = "tool"
	// KindModel is one call of a model through deck; like a tool it holds no agent slot.
	KindModel = "model"
)

type Config struct {
	Home    home.Layout
	MCPBase string
	Now     func() time.Time
	// Hold keeps a snapshot from copying the base between the row of a kept file and its blob, until the func it gives.
	Hold func() func()
	// AgentSlots is how many agent turns a workspace runs at once; scripts and tools do not count.
	AgentSlots func(ctx context.Context, workspace int64) int
	// Zone is the owner's zone of a workspace, whose wall clock a cron slot reads.
	Zone func(ctx context.Context, q *db.Queries, workspace int64) *time.Location
}

// Runner owns process lifecycles; its state is in the base. In memory it keeps handles and what a restart may lose,
// each such field saying what a restart does to it.
type Runner struct {
	st      *store.Store
	gw      *gateway.Gateway
	bus     *events.Bus
	scripts *script.Exec
	cfg     Config
	deps    Deps
	// tails keeps the lines Tail read of a log while the log stays as it was.
	tails tailCache

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
	// waking is a Wake under way; wakeAgain asks it for one more look.
	waking, wakeAgain bool
	// live are the turns this deck runs; a restart ends them and Recover resumes or interrupts their rows.
	live map[int64]*liveTurn
	// marked is when the output time of each open process was last written; after a restart the next output writes it.
	marked map[int64]time.Time
	// stderrCut says the log of an open process keeps no more of its stderr; after a restart the cut line comes again.
	stderrCut map[int64]bool
	// slots counts the agent turns of each workspace that hold a slot, taken or handed over; a restart starts at none.
	slots map[int64]int
	// hearing are the processes this deck closes and hears itself, which a drain of the outbox leaves alone; after a
	// restart the outbox has them all.
	hearing map[int64]bool
	// warned is the deadline each agent process was told it nears, negative while the telling is under way; after a
	// restart an agent near its ceiling is told again.
	warned map[int64]int64
}

// logged publishes that the log of a process grew, in its project: the log is a file, which no write of the base
// announces.
func (r *Runner) logged(ctx context.Context, processID int64) {
	r.bus.Emit(r.st.ProcessEvent(ctx, processID, events.Log))
}

type liveTurn struct {
	cancel context.CancelCauseFunc
	// done closes once the turn has exited and left its folder.
	done chan struct{}
	// input carries the owner's messages into the turn; nil while the provider has none.
	input chan provider.Message
	// slot is the agent slot the turn holds, under r.mu.
	slot *slot
	// node says the turn runs on a node, which gets the files of its folders as the turn starts, under r.mu.
	node bool
	// watch is the provider call under way, under r.mu.
	watch *watch
}

// Elsewhere runs scripts on another machine than deck's: side is deck's side of a script, emit tells the feed.
type Elsewhere interface {
	Run(ctx context.Context, j script.Job, side http.Handler, emit func(provider.Event)) ([]byte, error)
}

// Place is where the scripts of a project run: On, nil for deck's own machine, and the folders of the project there.
type Place struct {
	On      Elsewhere
	Folders []string
}

// Deps are what the runner takes from the rest of deck.
type Deps struct {
	Store   *store.Store
	Gateway *gateway.Gateway
	Bus     *events.Bus
	Scripts *script.Exec
	// Hooks hear of processes that close, park or end a turn, in this order.
	Hooks []Hook
	// For picks the provider a command starts on.
	For func(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Next is the provider of a command's assignment after the one that refused its turn; empty for none.
	Next func(ctx context.Context, projectID int64, cmd *manifest.Command, after string) string
	// Ahead is the provider of a command's assignment that is up and stands before the one a process runs on, or any
	// when that one is assigned no longer; empty for none.
	Ahead func(ctx context.Context, projectID int64, cmd *manifest.Command, current string) string
	// Named is the provider a process started on; its session belongs to it.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	// Goes reports whether a turn of the workspace may run on the provider now; the runner never picks.
	Goes func(ctx context.Context, workspace int64, name string) bool
	// Down is the status and text of the health of a provider that went on a pause or red, so no turn goes on
	// on it; empty when it is up.
	Down func(ctx context.Context, workspace int64, name string) (status, text string)
	// Broke hears that a turn on the provider failed or lost its login.
	Broke func(ctx context.Context, name string)
	// Limited pauses the scope of a provider's usage limit until ends (unix seconds, zero when the answer said no
	// reset) and says when the provider is called again; retry is a first limit without a reset, which pauses nothing.
	Limited func(ctx context.Context, workspace int64, name string, ends int64, words string) (until time.Time, retry bool)
	// Answered hears whether a turn of a provider answered and the windows of the subscription a turn of a CLI told;
	// Subscription is the connection of the CLI a provider runs on, empty for any other.
	Answered     func(ctx context.Context, workspace int64, name string, ok bool, ws []provider.Window)
	Subscription func(ctx context.Context, workspace int64, name string) string
	Prompt       func(ctx context.Context, projectID int64) (string, error)
	// Text is the current text of a project document of the kind, of any kind for ""; empty when missing.
	Text func(ctx context.Context, projectID int64, kind, name string) (string, error)
	// Skills are the files of the named skills: the project's own, else the workspace's, whose mark from holds.
	Skills func(ctx context.Context, projectID int64, names []string) (files map[string]string, from []Source, err error)
	// Servers starts the MCP servers of a project whose tools deck has not listed yet.
	Servers func(ctx context.Context, projectID int64)
	// Folder is the real path of the project folder an agent command works in, on the machine of its provider.
	Folder func(ctx context.Context, projectID int64, f manifest.Folder, provider string) (string, error)
	// Folders are the paths of the folders of a project on the machine of a node, deck's own for 0.
	Folders func(ctx context.Context, projectID, node int64) []string
	// ScriptsAt is where the scripts of a project run: on the node that holds its folders, else on deck.
	ScriptsAt func(ctx context.Context, projectID int64) (Place, error)
	// Memory is the memory a fresh session of the process gets, as data: the indexes and the lessons of its command
	// in the memory of its project; empty when there are none. again is the same turn asking anew, its reads counted.
	Memory func(ctx context.Context, p db.Process, again bool) (memory, lessons string, from []Source, err error)
	// Lang is the language the owner reads what a process says in, as a code; "" when deck does not know it.
	Lang func(ctx context.Context, workspace, process int64) string
	// Programs looks for the programs of the scripts of a project again and writes the health of its commands.
	Programs func(ctx context.Context, projectID int64) error
}

func New(cfg Config, deps Deps) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: deps.Store, gw: deps.Gateway, bus: deps.Bus, scripts: deps.Scripts, cfg: cfg, deps: deps, ctx: ctx, cancel: cancel,
		live: map[int64]*liveTurn{}, marked: map[int64]time.Time{}, stderrCut: map[int64]bool{}, warned: map[int64]int64{},
		slots: map[int64]int{}, hearing: map[int64]bool{}}
}

// Shutdown interrupts running providers and waits; interrupted processes stay running and resume on next start.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
}

func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) now() time.Time { return r.cfg.Now() }

func (r *Runner) stamp() sql.NullString {
	return store.At(r.now())
}

// spawn starts a turn unless deck is closing; the process then stays running and resumes on the next start,
// and the slot of the turn goes back. It reports whether the turn runs.
func (r *Runner) spawn(t turn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		t.slot.free()
		return false
	}
	ctx, cancel := context.WithCancelCause(r.ctx)
	lt := &liveTurn{cancel: cancel, done: make(chan struct{}), slot: t.slot}
	r.live[t.process] = lt
	t.live = lt
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			if r.live[t.process] == lt {
				delete(r.live, t.process)
			}
			r.mu.Unlock()
			cancel(nil)
			r.leave(context.WithoutCancel(ctx), t.process)
			close(lt.done)
		}()
		r.run(ctx, t)
	}()
	return true
}

// detailsCap is how much of the details of a failure a process keeps.
const detailsCap = 16000

// finish closes a running process done or failed, as apply asks; one that a stop has closed meanwhile stays as the stop left it.
func (r *Runner) finish(ctx context.Context, processID int64, o Outcome) {
	state, result, notBySchema, f := StateDone, "", false, (*fault.F)(nil)
	switch o := o.(type) {
	case Done:
		result, notBySchema = o.Result, o.NotBySchema
	case Fail:
		state, result, f = cmp.Or(o.State, StateError), o.Result, o.F
	default:
		panic(fmt.Sprintf("process: finish of %T", o))
	}
	nbs := int64(0)
	if notBySchema {
		nbs = 1
		f = cmp.Or(f, fault.New(fault.ResultInvalid, "the result did not match its schema"))
	}
	f = cmp.Or(f, &fault.F{})
	var n int64
	parked := false
	if !r.own(processID) {
		return
	}
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		// A message of the owner that came as the turn ended goes on in the same session instead.
		if state == StateDone {
			if m, err := q.ListUndeliveredRemarks(ctx, processID); err != nil || len(m) > 0 {
				if err == nil {
					parked, err = r.parkIn(ctx, q, processID)
				}
				return err
			}
		}
		// An agent runs again after only for a call it did not hear.
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		if p.Kind == KindAgent && p.Again != "" && !unheardCall(ctx, q, processID) {
			if err := q.SetAgain(ctx, db.SetAgainParams{ID: processID}); err != nil {
				return err
			}
		}
		// A failure that has no result of its own leaves the one handed in.
		if p.SubmittedAt.Valid && result == "" {
			result = p.Result
		}
		n, err = q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: f.Text,
			ErrorType: f.Type, ErrorDetails: untrusted.Cut(f.Details, detailsCap), FinishedAt: r.stamp(), ID: processID})
		if err != nil || n == 0 {
			return err
		}
		// A frozen call outlives its process unless the owner stops it.
		if _, err = q.CloseOpenAsksOfProcess(ctx, db.CloseOpenAsksOfProcessParams{ProcessID: processID, Now: store.Time(r.now())}); err != nil {
			return err
		}
		return q.ReleaseFolderLocks(ctx, processID)
	})
	if err != nil || parked || n == 0 {
		r.disown(processID)
	}
	if err != nil {
		slog.Error("finish process", "process", processID, "err", err)
		return
	}
	if parked {
		if err := r.Deliver(ctx, processID); err != nil {
			r.apply(ctx, processID, Fail{F: fault.Wrap("resume with a message", err)})
		}
		return
	}
	if n == 0 {
		return
	}
	r.Wake(ctx)
	r.mu.Lock()
	delete(r.marked, processID)
	delete(r.stderrCut, processID)
	r.mu.Unlock()
	r.closed(ctx, processID)
	r.disown(processID)
}

// unheardCall reports a call for its record deck gave a process that it did not take yet.
func unheardCall(ctx context.Context, q *db.Queries, processID int64) bool {
	waiting, _ := q.ListUndeliveredRemarks(ctx, processID)
	for _, m := range waiting {
		if m.Author == RemarkByDeck {
			return true
		}
	}
	return false
}

// closed announces a process that reached a final state and takes it off the outbox once every hook wrote what
// follows; a hook that failed leaves it there with one more try, heard again on the next drain.
func (r *Runner) closed(ctx context.Context, processID int64) {
	heard := r.hear(ctx, processID, func(h Hook, p db.Process) error { return h.Closed(ctx, p) })
	r.again(ctx, processID)
	if ctx.Err() != nil {
		// A deck that stops hears it on its next start, the try not counted.
		return
	}
	var tries int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if heard == nil {
			return q.DeleteClosedOutbox(ctx, processID)
		}
		var err error
		tries, err = q.FailClosedOutbox(ctx, db.FailClosedOutboxParams{Error: heard.Error(), Ref: processID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	// The health of the outbox tells the rest of the tries.
	if err != nil || heard != nil && (tries <= 1 || tries == StuckTries) {
		slog.Error("hear a closed process", "process", processID, "tries", tries, "err", errors.Join(heard, err))
	}
}

// own marks a process this deck is about to close and hear, false while another hears it.
func (r *Runner) own(processID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hearing[processID] {
		return false
	}
	r.hearing[processID] = true
	return true
}

func (r *Runner) disown(processID int64) {
	r.mu.Lock()
	delete(r.hearing, processID)
	r.mu.Unlock()
}

// DrainClosed hears the processes that closed and were not heard, such as those of a deck that died right after.
// Hooks hear a process at least once, so each acts once however often it hears it.
func (r *Runner) DrainClosed(ctx context.Context) {
	rows, err := r.st.Q.ListOutbox(ctx, "closed")
	if err != nil {
		return
	}
	for _, o := range rows {
		if r.own(o.Ref) {
			r.closed(ctx, o.Ref)
			r.disown(o.Ref)
		}
	}
}

// dropped journals the frozen calls a closed process took with it.
func (r *Runner) dropped(ctx context.Context, asks []db.Ask) {
	for _, a := range asks {
		if a.Kind == ask.KindCall {
			r.gw.Record(ctx, a, "rejected", "the process was stopped before the owner decided")
		}
	}
}

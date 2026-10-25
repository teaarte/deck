package process

import (
	"cmp"
	"context"
	"database/sql"
	"log/slog"
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
	StateRunning       = "running"
	StateWaitingAnswer = "waiting_answer"
	StateNeedsLogin    = "needs_login"
	// StateRateLimited is a pause on a provider's usage limit; StateWaitingProvider waits for a red self-check.
	StateRateLimited     = "rate_limited"
	StateWaitingProvider = "waiting_provider"
	// StateWaitingSlot waits for one of the agent turns the workspace may run at once.
	StateWaitingSlot = "waiting_slot"
	// StateWaitingFolder waits for another agent process to leave the project folder.
	StateWaitingFolder = "waiting_folder"
	StateDone          = "done"
	StateError         = "error"
	StateInterrupted   = "interrupted"
	StateStopped       = "stopped"
)

// Final are the states a process ends in; the client reads the same list from the catalog.
var Final = []string{StateDone, StateError, StateInterrupted, StateStopped}

// Ceiling is how long a command agent may work, ScriptCeiling a command script or tool, when the manifest sets
// no timeout; pauseProbe is how often a paused process tries again when its provider gave no reset time.
const (
	Ceiling       = time.Hour
	ScriptCeiling = 10 * time.Minute
	pauseProbe    = time.Hour
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
	// FilesMu keeps a snapshot from copying the base between the row of a kept file and its blob.
	FilesMu *sync.RWMutex
	// Hidden gives the path patterns an agent may not read or write.
	Hidden func() []string
	// AgentSlots is how many agent turns a workspace runs at once; scripts and tools do not count.
	AgentSlots func(ctx context.Context, workspace int64) int
}

// Runner owns process lifecycles. A process stops in waiting_answer when the agent is blocked on the
// owner; answers that arrive later are delivered in one resume message.
type Runner struct {
	st      *store.Store
	gw      *gateway.Gateway
	bus     *events.Bus
	scripts *script.Exec
	cfg     Config
	deps    Deps
	// Hooks hear of processes that close, park or end a turn, in this order.
	Hooks []Hook
	// lost counts the turns of a process that started without deck's tools; past lostTries it fails.
	lost map[int64]int
	// tails keeps the lines Tail read of a log while the log stays as it was.
	tails tailCache

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
	// waking is a Wake under way; wakeAgain asks it for one more look.
	waking, wakeAgain bool
	live              map[int64]*liveTurn
	// marked is when the output time of each open process was last written.
	marked map[int64]time.Time
	// slots counts the agent turns of each workspace that hold a slot, taken or handed over.
	slots map[int64]int
	// of is the workspace and project of each process an event was published for.
	of map[int64]events.Event
	// hearing are the processes this deck closes and hears itself, which a drain of the outbox leaves alone.
	hearing map[int64]bool
	// warned is the deadline each agent process was told it nears, negative while the telling is under way.
	warned map[int64]int64
}

// logged publishes that the log of a process grew, in its project: the log is a file, which no write of the base
// announces.
func (r *Runner) logged(ctx context.Context, processID int64) {
	r.mu.Lock()
	e, ok := r.of[processID]
	r.mu.Unlock()
	if !ok {
		if p, err := r.st.Q.GetProcess(ctx, processID); err == nil {
			if pr, err := r.st.Q.GetProject(ctx, p.ProjectID); err == nil {
				e = events.Event{Workspace: pr.WorkspaceID, Project: pr.Name}
				r.mu.Lock()
				r.of[processID] = e
				r.mu.Unlock()
			}
		}
	}
	e.Topic = "log"
	r.bus.Emit(e)
}

type liveTurn struct {
	cancel context.CancelCauseFunc
	// done closes once the turn has exited and left its folder.
	done chan struct{}
	// input carries the owner's messages into the turn; nil while the provider has none.
	input chan provider.Message
	// slot is the agent slot the turn holds, under r.mu.
	slot *slot
	// watch is the provider call under way, under r.mu.
	watch *watch
}

// Deps are what the runner takes from the rest of deck.
type Deps struct {
	// For picks the provider a command starts on.
	For func(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Next is the provider of a command's assignment after the one that refused its turn; empty for none.
	Next func(ctx context.Context, projectID int64, cmd *manifest.Command, after string) string
	// Named is the provider a process started on; its session belongs to it.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	// Goes reports whether a turn of the workspace may run on the provider now; the runner never picks.
	Goes func(ctx context.Context, workspace int64, name string) bool
	// Down is the status and text of the health of a provider that went on a pause or red, so no turn goes on
	// on it; empty when it is up.
	Down func(ctx context.Context, workspace int64, name string) (status, text string)
	// Broke hears that a turn on the provider failed or lost its login.
	Broke func(ctx context.Context, name string)
	// Recheck runs the self-check of the provider at the time a limit it met resets.
	Recheck func(name string, at time.Time)
	Prompt  func(ctx context.Context, projectID int64) (string, error)
	// Text is the current text of a project document of the kind, of any kind for ""; empty when missing.
	Text func(ctx context.Context, projectID int64, kind, name string) (string, error)
	// Skills are the files of the named skills: the project's own, else the workspace's.
	Skills func(ctx context.Context, projectID int64, names []string) (map[string]string, error)
	// Servers starts the MCP servers of a project whose tools deck has not listed yet.
	Servers func(ctx context.Context, projectID int64)
	// Folder is the real path of the project folder an agent command works in.
	Folder func(ctx context.Context, projectID int64, f manifest.Folder) (string, error)
	// Memory is the memory a fresh session of the process gets, as data; empty when there is none.
	Memory func(ctx context.Context, p db.Process) (string, error)
	// Programs looks for the programs of the scripts of a project again and writes the health of its commands.
	Programs func(ctx context.Context, projectID int64) error
}

func NewRunner(st *store.Store, gw *gateway.Gateway, bus *events.Bus, scripts *script.Exec, cfg Config, deps Deps) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: st, gw: gw, bus: bus, scripts: scripts, cfg: cfg, deps: deps, ctx: ctx, cancel: cancel,
		live: map[int64]*liveTurn{}, marked: map[int64]time.Time{}, warned: map[int64]int64{},
		slots: map[int64]int{}, of: map[int64]events.Event{}, hearing: map[int64]bool{}}
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
	return sql.NullString{String: store.Time(r.now()), Valid: true}
}

// spawn starts a turn unless deck is closing; the process then stays running and resumes on the next start,
// and the slot of the turn goes back. It reports whether the turn runs.
func (r *Runner) spawn(t turn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		if t.slot != nil {
			go t.slot.release(context.WithoutCancel(r.ctx))
		}
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

// finish closes a running process; one that a stop has closed meanwhile stays as the stop left it.
func (r *Runner) finish(ctx context.Context, processID int64, state, result string, notBySchema bool, f *fault.F) {
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
		if p, err := q.GetProcess(ctx, processID); err != nil {
			return err
		} else if p.Kind == KindAgent && p.Again != "" && !unheardCall(ctx, q, processID) {
			if err := q.SetAgain(ctx, db.SetAgainParams{ID: processID}); err != nil {
				return err
			}
		}
		var err error
		n, err = q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: f.Text,
			ErrorType: f.Type, ErrorDetails: untrusted.Cut(f.Details, detailsCap), FinishedAt: r.stamp(), ID: processID})
		if err != nil || n == 0 {
			return err
		}
		// A frozen call outlives its process unless the owner stops it.
		if _, err = q.CloseOpenAsksOfProcess(ctx, db.CloseOpenAsksOfProcessParams{ProcessID: processID}); err != nil {
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
			r.finish(ctx, processID, StateError, "", false, fault.Wrap("resume with a message", err))
		}
		return
	}
	if n == 0 {
		return
	}
	r.Wake(ctx)
	r.mu.Lock()
	delete(r.marked, processID)
	delete(r.lost, processID)
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

// closed announces a process that reached a final state, then takes it off the outbox; its log stays until deck
// clears old runs.
func (r *Runner) closed(ctx context.Context, processID int64) {
	r.mu.Lock()
	delete(r.of, processID)
	r.mu.Unlock()
	// A process deck could not read stays in the outbox, heard on the next drain.
	if err := r.hear(ctx, processID, func(h Hook, p db.Process) { h.Closed(ctx, p) }); err != nil {
		slog.Error("hear a closed process", "process", processID, "err", err)
		return
	}
	r.again(ctx, processID)
	if err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteClosedOutbox(ctx, processID) }); err != nil {
		slog.Error("outbox", "process", processID, "err", err)
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

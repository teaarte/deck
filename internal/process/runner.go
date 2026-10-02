package process

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
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

// Ceiling is how long a command agent may work, ScriptCeiling a command script or tool, when the manifest sets
// no timeout; pauseProbe is how often a paused process tries again when its provider gave no reset time.
const (
	Ceiling       = time.Hour
	ScriptCeiling = 10 * time.Minute
	pauseProbe    = time.Hour
)

// MaxDepth is the longest chain of processes that start one another.
const MaxDepth = 5

// Empty is the result of a process that had nothing to do.
const Empty = "null"

const (
	KindAgent  = "agent"
	KindScript = "script"
	KindTool   = "tool"
)

type Config struct {
	RunsDir  string
	MCPBase  string
	Now      func() time.Time
	FilesDir string
	// Hidden gives the path patterns an agent of a project and process may not read or write.
	Hidden func(project, process int64) []string
	// PauseLimit is how long one pause on a usage limit may last from the first refusal.
	PauseLimit time.Duration
	// AgentSlots is how many agent turns a workspace runs at once; scripts and tools do not count.
	AgentSlots func(ctx context.Context, workspace int64) int
}

// Runner owns process lifecycles. A process stops in waiting_answer when the agent is blocked on the
// owner; answers that arrive later are delivered in one resume message.
type Runner struct {
	st        *store.Store
	gw        *gateway.Gateway
	bus       *events.Bus
	providers Providers
	scripts   *script.Exec
	cfg       Config
	projects  Projects
	// Hooks hear of processes that close, park or end a turn, in this order.
	Hooks []Hook

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
	live    map[int64]*liveTurn
	// marked is when the output time of each open process was last written.
	marked map[int64]time.Time
	// slots counts the agent turns of each workspace that hold a slot, taken or handed over.
	slots map[int64]int
}

type liveTurn struct {
	cancel context.CancelCauseFunc
	// input carries the owner's messages into the turn; nil while the provider has none.
	input chan provider.Message
}

// Providers gives the runner the provider of a process.
type Providers interface {
	// For picks the provider a command starts on.
	For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Named is the provider a process started on; its session belongs to it.
	Named(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	// Ready reports whether a turn may start on the provider; not while its self-check runs after a change or is red.
	Ready(ctx context.Context, name string) bool
}

// Projects gives the runner the current manifest and md prompt of a project.
type Projects interface {
	Prompt(ctx context.Context, projectID int64) (string, error)
	// Text is the current text of a project document of the kind, of any kind for ""; empty when missing.
	Text(ctx context.Context, projectID int64, kind, name string) (string, error)
	// Skills are the files of the named skills: the project's own, else the workspace's.
	Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error)
	// Servers starts the MCP servers of a project whose tools deck has not listed yet.
	Servers(ctx context.Context, projectID int64)
	// Folder is the real path of the project folder an agent command works in.
	Folder(ctx context.Context, projectID int64, f manifest.Folder) (string, error)
	// Memory is the memory a fresh session of the process gets, as data; empty when there is none.
	Memory(ctx context.Context, p db.Process) (string, error)
}

func NewRunner(st *store.Store, gw *gateway.Gateway, bus *events.Bus, p Providers, scripts *script.Exec, cfg Config, projects Projects) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: st, gw: gw, bus: bus, providers: p, scripts: scripts, cfg: cfg, projects: projects, ctx: ctx, cancel: cancel,
		live: map[int64]*liveTurn{}, marked: map[int64]time.Time{},
		slots: map[int64]int{}}
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
	lt := &liveTurn{cancel: cancel}
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
		}()
		r.run(ctx, t)
	}()
	return true
}

// finish closes a running process; one that a stop has closed meanwhile stays as the stop left it.
func (r *Runner) finish(ctx context.Context, processID int64, state, result string, notBySchema bool, errText string) {
	nbs := int64(0)
	if notBySchema {
		nbs = 1
	}
	var n int64
	var closedCalls []db.Ask
	parked := false
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
		var err error
		n, err = q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: errText,
			FinishedAt: r.stamp(), ID: processID})
		if err != nil || n == 0 {
			return err
		}
		closedCalls, err = q.CloseOpenAsksOfProcess(ctx, db.CloseOpenAsksOfProcessParams{ProcessID: processID, CallsToo: b2i(state == StateStopped)})
		if err != nil {
			return err
		}
		return q.ReleaseFolderLocks(ctx, processID)
	})
	if err != nil {
		slog.Error("finish process", "process", processID, "err", err)
		return
	}
	if parked {
		r.bus.Publish("processes")
		if err := r.Deliver(ctx, processID); err != nil {
			r.finish(ctx, processID, StateError, "", false, "resume with a message: "+err.Error())
		}
		return
	}
	if n == 0 {
		return
	}
	r.Wake(ctx)
	r.mu.Lock()
	delete(r.marked, processID)
	r.mu.Unlock()
	r.dropped(ctx, closedCalls)
	r.closed(ctx, processID)
}

// closed announces a process that reached a final state; its log stays until deck clears old runs.
func (r *Runner) closed(ctx context.Context, processID int64) {
	r.bus.Publish("processes")
	r.bus.Publish("asks")
	r.hear(ctx, processID, func(h Hook, p db.Process) { h.Closed(ctx, p) })
}

// dropped journals the frozen calls a closed process took with it.
func (r *Runner) dropped(ctx context.Context, asks []db.Ask) {
	for _, a := range asks {
		if a.Kind == ask.KindCall {
			r.gw.Record(ctx, a, "rejected", "the process was stopped before the owner decided")
		}
	}
}

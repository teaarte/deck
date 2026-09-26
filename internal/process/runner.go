package process

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"sync"
	"time"

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
	StateDone          = "done"
	StateError         = "error"
	StateInterrupted   = "interrupted"
	StateStopped       = "stopped"
)

// MaxDepth is the longest chain of processes that start one another.
const MaxDepth = 5

// Empty is the result of a process that had nothing to do.
const Empty = "null"

const (
	KindAgent  = "agent"
	KindScript = "script"
)

type Config struct {
	RunsDir string
	MCPBase string
	Now     func() time.Time
	// Ceiling is how long a command agent may work when its manifest sets no timeout.
	Ceiling time.Duration
	// ScriptCeiling is how long a command script may run when its manifest sets no timeout.
	ScriptCeiling time.Duration
	FilesDir      string
}

// Runner owns process lifecycles. A process stops in waiting_answer when the agent is blocked on the
// owner; answers that arrive later are delivered in one resume message.
type Runner struct {
	st       *store.Store
	gw       *gateway.Gateway
	bus      *events.Bus
	provider provider.Provider
	scripts  *script.Exec
	cfg      Config
	projects Projects
	// OnDone runs after a process finishes done with a result that is not empty and fits its schema.
	OnDone func(ctx context.Context, p db.Process)

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
	live    map[int64]*liveTurn
	// stopping holds stops of processes whose live turn was cancelled; a turn spawned after it does not run.
	stopping map[int64]string
}

type liveTurn struct{ cancel context.CancelCauseFunc }

// Projects gives the runner the current manifest and md prompt of a project.
type Projects interface {
	Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error)
	// ManifestAt is a stored revision of the manifest; 0 is the current one.
	ManifestAt(ctx context.Context, projectID, revision int64) (*manifest.Manifest, error)
	Prompt(ctx context.Context, projectID int64) (string, error)
	// Text is the current text of a project document of the kind, of any kind for ""; empty when missing.
	Text(ctx context.Context, projectID int64, kind, name string) (string, error)
	// Skills are the files of the named skills: the project's own, else the workspace's.
	Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error)
}

func NewRunner(st *store.Store, gw *gateway.Gateway, bus *events.Bus, p provider.Provider, scripts *script.Exec, cfg Config, projects Projects) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: st, gw: gw, bus: bus, provider: p, scripts: scripts, cfg: cfg, projects: projects, ctx: ctx, cancel: cancel,
		live: map[int64]*liveTurn{}, stopping: map[int64]string{}}
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

// spawn starts a turn unless deck is stopping; the process then stays running and resumes on the next start.
func (r *Runner) spawn(t turn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if reason, ok := r.stopping[t.process]; ok {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.finish(context.WithoutCancel(r.ctx), t.process, StateStopped, "", false, reason)
		}()
		return
	}
	ctx, cancel := context.WithCancelCause(r.ctx)
	lt := &liveTurn{cancel: cancel}
	r.live[t.process] = lt
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
		}()
		r.run(ctx, t)
	}()
}

// finish closes a running process; one that a stop has closed meanwhile stays as the stop left it.
func (r *Runner) finish(ctx context.Context, processID int64, state, result string, notBySchema bool, errText string) {
	nbs := int64(0)
	if notBySchema {
		nbs = 1
	}
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: errText,
			FinishedAt: r.stamp(), ID: processID})
		if err != nil || n == 0 {
			return err
		}
		return q.CloseOpenAsksOfProcess(ctx, processID)
	})
	if err != nil {
		slog.Error("finish process", "process", processID, "err", err)
		return
	}
	if n == 0 {
		return
	}
	r.mu.Lock()
	delete(r.stopping, processID)
	r.mu.Unlock()
	r.closed(processID)
	if state == StateDone && !notBySchema && result != Empty && r.OnDone != nil {
		if p, err := r.st.Q.GetProcess(ctx, processID); err == nil {
			r.OnDone(ctx, p)
		}
	}
}

// settle takes a turn off the live ones before it writes its outcome: a stop that came first wins, a stop
// that comes later finds no live turn and closes the process in the base, so the outcome write does nothing.
func (r *Runner) settle(processID int64) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, processID)
	reason, ok := r.stopping[processID]
	return reason, ok
}

// stopFirst ends a turn as stopped when a stop came before its outcome.
func (r *Runner) stopFirst(ctx context.Context, processID int64) bool {
	reason, ok := r.settle(processID)
	if ok {
		r.finish(ctx, processID, StateStopped, "", false, reason)
	}
	return ok
}

// closed drops the log of a process that reached a final state and announces it.
func (r *Runner) closed(processID int64) {
	_ = os.Remove(r.LogPath(processID))
	r.bus.Publish("processes")
	r.bus.Publish("asks")
}

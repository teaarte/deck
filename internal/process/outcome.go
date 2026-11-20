package process

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

// Outcome is how a run of a process ends, the closed set apply acts on.
type Outcome interface{ outcome() }

// Done ends the process done with Result; NotBySchema ends it off its schema, which nothing acts on, telling Warn.
type Done struct {
	Result      string
	NotBySchema bool
	Warn        string
}

// Fail ends the process in State, error when empty, with F and the Result it has; Broke names a provider that
// hears its turn failed.
type Fail struct {
	F      *fault.F
	Result string
	State  string
	Broke  string
}

// Settle closes a run with what it gave, the text of its turn heard first: a failure, or a result kept by its schema.
type Settle struct {
	Run    *run
	Result string
	F      *fault.F
	Text   string
	Said   bool
}

// Park waits for the owner's answer, the text of its turn heard first when Said.
type Park struct {
	Text string
	Said bool
}

// Pause waits on the usage limit of its provider until Until.
type Pause struct {
	Provider provider.Provider
	Until    time.Time
}

// Limit is a usage limit of the provider of a turn: a short one of a subscription waits on the same login, else the
// work goes on on the next model, else it pauses.
type Limit struct {
	Turn      turn
	Provider  provider.Provider
	Workspace int64
	ResetsAt  int64
	Text      string
}

// NeedLogin waits for the owner to log the provider in again.
type NeedLogin struct {
	Provider provider.Provider
	Text     string
}

// HandOn closes the process and goes on with its work on the next model; Else when there is none. Broke tells the
// provider it handed on from that its turn failed.
type HandOn struct {
	Turn  turn
	From  provider.Provider
	F     *fault.F
	Broke bool
	Else  Outcome
}

// Hold waits, in its session, for a provider that went down, telling the owner Warn.
type Hold struct {
	Turn     turn
	Provider string
	Warn     string
}

// LostTools holds a turn whose agent did not get deck's tools until the self-check of its provider is green again.
type LostTools struct {
	Turn     turn
	Provider provider.Provider
	Err      error
}

// Gone is a process a stop or another end closed meanwhile: nothing more.
type Gone struct{}

func (Done) outcome()      {}
func (Fail) outcome()      {}
func (Settle) outcome()    {}
func (Park) outcome()      {}
func (Pause) outcome()     {}
func (Limit) outcome()     {}
func (NeedLogin) outcome() {}
func (HandOn) outcome()    {}
func (Hold) outcome()      {}
func (LostTools) outcome() {}
func (Gone) outcome()      {}

// apply makes the outcome of a process, the one place a process ends.
func (r *Runner) apply(ctx context.Context, processID int64, o Outcome) {
	switch o := o.(type) {
	case Done, Fail:
		r.finish(ctx, processID, o)
		if d, ok := o.(Done); ok && d.Warn != "" {
			r.warn(ctx, processID, d.Warn)
		}
		if f, ok := o.(Fail); ok && f.Broke != "" {
			r.deps.Broke(ctx, f.Broke)
		}
	case Settle:
		if o.Said {
			r.turnText(ctx, processID, o.Text)
		}
		r.apply(ctx, processID, r.settled(ctx, o.Run, o.Result, o.F))
	case Park:
		if o.Said {
			r.turnText(ctx, processID, o.Text)
		}
		r.park(ctx, processID)
	case Pause:
		r.pause(ctx, o.Provider, processID, o.Until)
	case Limit:
		until, _ := r.deps.Limited(ctx, o.Workspace, o.Provider.Name(), o.ResetsAt, o.Text)
		pause := Pause{Provider: o.Provider, Until: until}
		// A short limit of a subscription waits on the same login: another one would take the burst and lose the cache.
		if r.deps.Subscription(ctx, o.Workspace, o.Provider.Name()) != "" && !until.After(r.now().Add(shortLimit)) {
			r.apply(ctx, processID, pause)
			return
		}
		r.apply(ctx, processID, HandOn{Turn: o.Turn, From: o.Provider, F: fault.With(fault.ModelLimit, fmt.Sprintf("%s reached its usage limit", o.Provider.Name()), o.Text), Else: pause})
	case NeedLogin:
		r.needLogin(ctx, o.Provider, processID, o.Text)
	case HandOn:
		if !r.handOn(ctx, o.Turn, o.From, o.F) {
			r.apply(ctx, processID, o.Else)
			return
		}
		if o.Broke {
			r.deps.Broke(ctx, o.From.Name())
		}
	case Hold:
		r.warnOf(ctx, processID, o.Warn, health.Provider(o.Provider))
		r.hold(ctx, o.Turn, wait{reason: WaitProvider, on: o.Provider, silence: SilenceFail + fault.ModelUnavailable, until: r.now().Add(providerTerm)})
	case LostTools:
		r.lostTools(ctx, o.Turn, o.Provider, o.Err)
	case Gone:
	default:
		panic(fmt.Sprintf("process: apply of %T", o))
	}
}

// facts are what a turn of an agent knows as its run of the provider returns; heard are known once the answer is
// taken in.
type facts struct {
	t         turn
	pv        provider.Provider
	run       *run
	workspace int64
	res       provider.Result
	err       error
	// left is why deck took the turn from its model, nil when it did not or a result was handed in.
	left *leaving
	// cut is a stop or deck ending; running is the state of the process in the base.
	cut, running bool
	// sub says the turn hands its result in by deck's tool; taken that the schema took it, submitted that one is in.
	sub, taken, submitted bool
	// resume and prompt are of the run that returned; session is the session the turn has.
	resume  bool
	prompt  string
	session string
	// rejoined, revived and retried are the runs again the turn already had.
	rejoined, revived, retried bool
	// questioned is a question to the owner still open.
	questioned bool
	heard      *heard
}

// heard is what deck knows of a turn once it took its answer in.
type heard struct {
	// blocked says the owner still owes the agent an answer; invalid is why the result does not fit its schema.
	blocked bool
	invalid error
}

// rerun is an outcome of a turn that does not end it: the provider runs once more in the same turn.
type rerun struct {
	Why string
	// Text is the error of the result the turn is asked again for, or the kill it goes on after.
	Text string
}

// takeIn is the end of a run whose answer deck takes in before the rules go on: its tells and the health of its
// provider, with the result handed in beating a failure after it.
type takeIn struct {
	res provider.Result
	err error
}

func (rerun) outcome()  {}
func (takeIn) outcome() {}

// rule is one rule of how a turn of an agent ends: its outcome when it holds of the facts, else nil.
type rule func(f *facts) Outcome

// rules are how a turn of an agent ends, in order: the first that holds decides.
var rules = []rule{
	leftItsModel, wasCut, lostItsSession, beganWithoutTools, hasNoTools,
	takenIn,
	questionWaits,
	killedOnce, killed, failedToRun, pastALimitOfDeck, modelUnavailable, usageLimit, lostLogin, agentFailed, apiRefused,
	answerOwed, fits, offSchemaAgain, offSchema,
}

// classify is how a turn of an agent ends by the facts of its last run of the provider.
func classify(f *facts) Outcome {
	for _, rule := range rules {
		if o := rule(f); o != nil {
			return o
		}
	}
	panic("process: no rule decided the end of a turn")
}

// leftItsModel: deck took the turn from its model and no result was handed in.
func leftItsModel(f *facts) Outcome {
	if f.left == nil {
		return nil
	}
	return leave(f)
}

// wasCut: a stop or deck ending cut the turn; nothing more.
func wasCut(f *facts) Outcome {
	if f.cut {
		return Gone{}
	}
	return nil
}

// lostItsSession: the session of a resume was gone, so the turn runs again in a fresh one.
func lostItsSession(f *facts) Outcome {
	if f.heard == nil && f.err == nil && f.res.SessionMissing {
		return rerun{Why: "session"}
	}
	return nil
}

// beganWithoutTools: the session began before deck's tools reached it, so it goes on in that session once.
func beganWithoutTools(f *facts) Outcome {
	if f.heard == nil && f.err != nil && f.res.Is(fault.DeckTools) && f.res.Begun && !f.rejoined {
		return rerun{Why: "tools"}
	}
	return nil
}

// hasNoTools: deck's tools did not reach the agent, so the turn waits for the self-check of its provider.
func hasNoTools(f *facts) Outcome {
	if f.heard != nil || f.err == nil || !f.res.Is(fault.DeckTools) {
		return nil
	}
	held := f.t
	held.resume = f.resume
	if held.resume {
		held.message = f.prompt
	}
	return LostTools{Turn: held, Provider: f.pv, Err: f.err}
}

// takenIn: deck takes the answer in before the rules after it, a result handed in beating a failure after it.
func takenIn(f *facts) Outcome {
	if f.heard != nil {
		return nil
	}
	res, err := handedIn(f)
	return takeIn{res: res, err: err}
}

// questionWaits: a question that waits for the owner beats a failure and a hand-on; the turn parks.
func questionWaits(f *facts) Outcome {
	if (f.err != nil || f.res.Fault != nil) && f.questioned {
		return Park{}
	}
	return nil
}

// killedOnce: a signal from outside cut the turn, which goes on in its session once.
func killedOnce(f *facts) Outcome {
	var k *provider.Killed
	if errors.As(f.err, &k) && !f.revived && f.session != "" {
		return rerun{Why: "killed", Text: k.Error()}
	}
	return nil
}

// killed: cut from outside again, the turn fails.
func killed(f *facts) Outcome {
	var k *provider.Killed
	if errors.As(f.err, &k) {
		return Fail{F: fault.With(fault.Killed, k.Error(), k.Stderr)}
	}
	return nil
}

// failedToRun: the provider failed to run the turn.
func failedToRun(f *facts) Outcome {
	if f.err != nil {
		return Fail{F: fault.Of(f.err), Broke: f.pv.Name()}
	}
	return nil
}

// pastALimitOfDeck: deck held the call back, so the model is not at a limit of its own and does not pause.
func pastALimitOfDeck(f *facts) Outcome {
	if f.res.Past == "" {
		return nil
	}
	lf := fault.With(fault.ModelLimit, untrusted.Flat(f.res.Past, 300), f.res.Past)
	return HandOn{Turn: f.t, From: f.pv, F: lf, Else: Fail{F: lf}}
}

// modelUnavailable: the work goes on on the next model, else the turn fails.
func modelUnavailable(f *facts) Outcome {
	if !f.res.Is(fault.ModelUnavailable) {
		return nil
	}
	text := f.res.Fault.Text
	uf := fault.With(fault.ModelUnavailable, fmt.Sprintf("%s did not answer: %s", f.pv.Name(), untrusted.Flat(text, 200)), text)
	return HandOn{Turn: f.t, From: f.pv, F: uf, Broke: true, Else: Fail{F: uf, Result: text, Broke: f.pv.Name()}}
}

// usageLimit: a usage limit of the provider.
func usageLimit(f *facts) Outcome {
	if !f.res.Is(fault.ModelLimit) {
		return nil
	}
	return Limit{Turn: f.t, Provider: f.pv, Workspace: f.workspace, ResetsAt: f.res.ResetsAt, Text: f.res.Fault.Text}
}

// lostLogin: the turn waits for the owner to log its provider in again.
func lostLogin(f *facts) Outcome {
	if !f.res.Is(fault.ModelLogin) {
		return nil
	}
	return NeedLogin{Provider: f.pv, Text: f.res.Fault.Text}
}

// agentFailed: the agent reported an error.
func agentFailed(f *facts) Outcome {
	if f.res.Fault == nil {
		return nil
	}
	text := f.res.Fault.Text
	return Fail{F: fault.With(fault.TaskFailed, fmt.Sprintf("the agent reported an error: %s", untrusted.Flat(text, 200)), text), Result: text, Broke: f.pv.Name()}
}

// apiRefused: the model API refused a call and no result fits, so the work goes on on the next model, else it fails.
func apiRefused(f *facts) Outcome {
	if f.heard.blocked || f.heard.invalid == nil || f.res.Refusal == "" {
		return nil
	}
	rf := fault.With(fault.ModelUnavailable, fmt.Sprintf("%s refused a call of the turn: %s", f.pv.Name(), untrusted.Flat(f.res.Refusal, 200)), f.res.Refusal)
	return HandOn{Turn: f.t, From: f.pv, F: rf, Broke: true, Else: Fail{F: rf, Result: f.res.Refusal, Broke: f.pv.Name()}}
}

// answerOwed: the owner still owes the agent an answer, so a result produced meanwhile is premature and the turn parks.
func answerOwed(f *facts) Outcome {
	if f.heard.blocked {
		return Park{Text: f.res.Text, Said: true}
	}
	return nil
}

// fits: the result fits its schema and the run settles.
func fits(f *facts) Outcome {
	if f.heard.invalid != nil {
		return nil
	}
	result := f.res.Text
	if len(f.t.cmd.Result) > 0 {
		result = string(f.res.Structured)
	}
	return Settle{Run: f.run, Result: result, Text: f.res.Text, Said: true}
}

// offSchemaAgain: off its schema the second time, the result ends done and nothing acts on it.
func offSchemaAgain(f *facts) Outcome {
	if !f.retried {
		return nil
	}
	raw := string(f.res.Structured)
	if raw == "" {
		raw = f.res.Text
	}
	return Done{Result: raw, NotBySchema: true}
}

// offSchema: a result off its schema is asked for once more.
func offSchema(f *facts) Outcome {
	return rerun{Why: "result", Text: f.heard.invalid.Error()}
}

// leave is how a turn deck took from its model ends: a waiting question parks it, else the work goes on on the next
// model, else a turn on a provider that went down waits for it in its session, any other fails.
func leave(f *facts) Outcome {
	l := f.left
	switch {
	case !f.running:
		return Gone{}
	case f.questioned:
		return Park{}
	}
	unavailable := l.f.Type == fault.ModelUnavailable
	var otherwise Outcome = Fail{F: l.f}
	if unavailable {
		otherwise = Fail{F: l.f, Broke: f.pv.Name()}
	}
	if l.down != "" {
		held := f.t
		held.resume, held.message = true, "deck: your model was held up and is back. Continue where you stopped."
		otherwise = Hold{Turn: held, Provider: f.pv.Name(), Warn: fmt.Sprintf("%s waits for %s: %s", cmp.Or(f.t.cmd.Title, f.t.cmd.Name), f.pv.Name(), l.f.Text)}
	}
	return HandOn{Turn: f.t, From: f.pv, F: l.f, Broke: l.down == "" && unavailable, Else: otherwise}
}

// handedIn is the end of a run as deck takes it: a result handed in beats a failure after it.
func handedIn(f *facts) (provider.Result, error) {
	res, err := f.res, f.err
	var killed *provider.Killed
	if f.taken {
		// A turn that handed in a result its schema took is done, whatever came after it.
		err, res.Fault, res.Past, res.Refusal = nil, nil, "", ""
	}
	if errors.As(err, &killed) && f.submitted {
		// A turn killed after it handed in its result is done.
		err, res.Fault = nil, nil
	}
	if f.sub && err == nil && !res.Is(fault.TaskFailed) && f.submitted {
		// A turn that ended on its own with its result in is done, whatever a later call of the model got.
		res.Fault = nil
	}
	return res, err
}

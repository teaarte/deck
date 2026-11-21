package registry

import "encoding/json"

// Effect is what a call did: done, started a process, waits for the owner's answer, or changed nothing as it is so
// already. A refusal is an error, not an effect.
type Effect string

const (
	Done      Effect = "done"
	Started   Effect = "started"
	Waiting   Effect = "waiting"
	Unchanged Effect = "unchanged"
)

// Result is what a call gives: its effect, the data by the command's result schema, the process it started and the
// ask it waits on. Its JSON is the data alone.
type Result struct {
	Effect  Effect
	Data    any
	Process int64
	Ask     int64
}

func (r Result) MarshalJSON() ([]byte, error) { return json.Marshal(r.Data) }

// Effected is the data of a call that says its own effect, with the process it started or the ask it waits on.
type Effected interface{ Effect() (Effect, int64) }

// ResultOf is the result of a call whose handler gave out: a result as it is, the effect data says of itself, else
// done.
func ResultOf(out any) Result {
	switch v := out.(type) {
	case Result:
		return v
	case Effected:
		e, id := v.Effect()
		r := Result{Effect: e, Data: out}
		switch e {
		case Started:
			r.Process = id
		case Waiting:
			r.Ask = id
		}
		return r
	}
	return Result{Effect: Done, Data: out}
}

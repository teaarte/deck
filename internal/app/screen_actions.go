package app

import "deck2/internal/store/db"

// confirmed is a button the client asks the owner about before it calls.
func confirmed(act map[string]any) map[string]any {
	act["confirm"] = true
	return act
}

// call is a button that calls a command; verb names its label for the client.
func call(verb string, primary bool, command string, params map[string]any) map[string]any {
	out := map[string]any{"verb": verb, "command": command, "params": params}
	if primary {
		out["primary"] = true
	}
	return out
}

func withInput(act map[string]any, param string) map[string]any {
	act["input"] = param
	return act
}

// until hides a button once its confirmation expires.
func until(act map[string]any, q db.Ask) map[string]any {
	if q.ExpiresAt.Valid {
		act["until"] = q.ExpiresAt.String
	}
	return act
}

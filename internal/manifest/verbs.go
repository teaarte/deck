package manifest

import (
	"bytes"
	"encoding/json"
)

// button is the label of a built-in button and the Tabler icon it wears without a label of its own.
type button struct{ Label, Icon string }

// verbs are the built-in buttons of deck's screens and data: a button names one instead of a label.
var verbs = map[string]button{
	"close": {"Close", ""}, "edit": {"Edit", "pencil"}, "replace": {"Replace", "replace"}, "models": {"Models", ""},
	"model": {"Model", ""}, "fallbacks": {"Fallbacks", ""}, "try": {"Try", "flask"}, "set": {"Set", "key"},
	"answer": {"Answer", "message"}, "allow": {"Allow", "check"}, "allow_always": {"Always allow here", ""},
	"deny": {"Deny", "x"}, "deny_because": {"Deny because…", ""}, "retry": {"Retry", "refresh"},
	"mark_done": {"Count as done", "check"}, "mark_read": {"Mark read", "check"}, "read_all": {"Read all", ""},
	"mark": {"Mark", ""}, "new_session": {"New session", ""}, "continue": {"Continue", "player-play"},
	"sign_in": {"Sign in", "login"}, "signed_in": {"Signed in", ""}, "undo": {"Undo", "arrow-back-up"},
	"stop": {"Stop", "player-stop"}, "check": {"Check", ""}, "open": {"Open", "arrow-up-right"},
	"run_now": {"Run now", "player-play"}, "pause": {"Pause", "player-pause"}, "restore": {"Bring back", ""},
	"resume": {"Resume", "player-play"}, "turn_off": {"Turn off", ""}, "turn_on": {"Turn on", ""},
	"send": {"Send", "send"}, "rework": {"Send back for rework", ""}, "enable": {"Enable project", ""},
	"review": {"Review and enable", ""}, "export": {"Export", ""}, "revoke": {"Revoke", ""},
	"remove": {"Remove", "trash"}, "delete": {"Delete", "trash"}, "download": {"Download", "download"},
	"cancel": {"Cancel", ""}, "archive": {"Archive", "archive"}, "tell": {"Tell it", "message"},
	"reset_push": {"Reset the push key", ""}, "settings": {"Settings", "settings"}, "unarchive": {"Bring back", ""},
	"revert": {"Back to this", ""}, "save": {"Save", ""}, "use": {"Use", ""},
}

// IsVerb says deck has a built-in button of this name.
func IsVerb(name string) bool { _, ok := verbs[name]; return ok }

// Labeled is a JSON value with every button that names a verb given its label in ws and, without a label of its
// own, its icon; a value with no verb comes back as it is.
func Labeled(raw []byte, ws map[string]string) []byte {
	if !bytes.Contains(raw, []byte(`"verb":`)) {
		return raw
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return raw
	}
	out, err := json.Marshal(label(v, ws))
	if err != nil {
		return raw
	}
	return out
}

func label(v any, ws map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			x[k] = label(c, ws)
		}
		name, _ := x["verb"].(string)
		vb, ok := verbs[name]
		if !ok {
			return x
		}
		if _, own := x["label"]; !own {
			x["label"] = Tr(ws, vb.Label)
			if _, icon := x["icon"]; !icon && vb.Icon != "" {
				x["icon"] = vb.Icon
			}
		}
	case []any:
		for i, c := range x {
			x[i] = label(c, ws)
		}
	}
	return v
}

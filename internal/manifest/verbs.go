package manifest

import (
	"bytes"
	"encoding/json"
)

// button is the label of a built-in button and the Tabler icon it wears without a label of its own.
type button struct{ Label, Icon string }

// verbs are the built-in buttons of deck's screens and data: a button names one instead of a label.
var verbs = map[string]button{
	"close": {"Close", "x"}, "edit": {"Edit", "pencil"}, "replace": {"Replace", "replace"}, "models": {"Models", "cpu"},
	"model": {"Change the model", "switch-horizontal"}, "fallbacks": {"Fallbacks", "list-numbers"}, "try": {"Try", "flask"}, "set": {"Set", "key"},
	"answer": {"Answer", "message"}, "allow": {"Allow", "check"}, "allow_always": {"Always allow here", "checks"},
	"deny": {"Deny", "x"}, "deny_because": {"Deny because…", "message-x"}, "retry": {"Retry", "refresh"},
	"mark_done": {"Count as done", "check"}, "mark_read": {"Mark read", "check"}, "read_all": {"Read all", "checks"},
	"mark": {"Mark", "bookmark"}, "new_session": {"New session", "message-plus"}, "continue": {"Continue", "player-play"},
	"sign_in": {"Sign in", "login"}, "signed_in": {"Signed in", "check"}, "undo": {"Undo", "arrow-back-up"},
	"stop": {"Stop", "player-stop"}, "check": {"Check", "stethoscope"}, "open": {"Open", "arrow-up-right"},
	"run_now": {"Run now", "player-play"}, "pause": {"Pause", "player-pause"}, "restore": {"Bring back", "arrow-back-up"},
	"resume": {"Resume", "player-play"}, "turn_off": {"Turn off", "power"}, "turn_on": {"Enable", "power"},
	"send": {"Send", "send"}, "rework": {"Send back for rework", "repeat"}, "enable": {"Enable project", "player-play"},
	"review": {"Review and enable", "eye-check"}, "export": {"Export", "file-export"}, "revoke": {"Revoke", "ban"},
	"remove": {"Remove", "trash"}, "delete": {"Delete", "trash"}, "download": {"Download", "download"},
	"cancel": {"Cancel", "x"}, "archive": {"Archive", "archive"}, "tell": {"Tell it", "message"},
	"reset_push": {"Reset the push key", "bell-x"}, "settings": {"Settings", "settings"}, "unarchive": {"Bring back", "archive-off"},
	"revert": {"Back to this", "history"}, "save": {"Save", "device-floppy"}, "use": {"Use", "check"},
}

// IsVerb says deck has a built-in button of this name.
func IsVerb(name string) bool { _, ok := verbs[name]; return ok }

// Labeled is a JSON value with every button that names a verb given its label in ws and, without a label of its
// own, its icon; the keys keep their order, and a value with no verb comes back as it is.
func Labeled(raw []byte, ws map[string]string) []byte {
	if !bytes.Contains(raw, []byte(`"verb":`)) {
		return raw
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	out, err := labeled(d, ws)
	if err != nil {
		return raw
	}
	return out
}

// labeled copies the next value of d, giving each button of a verb its label and icon.
func labeled(d *json.Decoder, ws map[string]string) ([]byte, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('['):
		out := []byte{'['}
		for d.More() {
			v, err := labeled(d, ws)
			if err != nil {
				return nil, err
			}
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(out, v...)
		}
		_, err = d.Token()
		return append(out, ']'), err
	case json.Delim('{'):
		out := []byte{'{'}
		var name string
		own, icon := false, false
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := labeled(d, ws)
			if err != nil {
				return nil, err
			}
			key, _ := k.(string)
			switch key {
			case "verb":
				_ = json.Unmarshal(v, &name)
			case "label":
				// An empty label is none: the verb gives it.
				if own = string(v) != `""`; !own {
					continue
				}
			case "icon":
				icon = true
			}
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(append(append(out, JSON(key, "")...), ':'), v...)
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		if vb, ok := verbs[name]; ok && !own {
			if len(out) > 1 {
				out = append(out, ',')
			}
			out = append(append(out, `"label":`...), JSON(Tr(ws, vb.Label), "")...)
			if !icon && vb.Icon != "" {
				out = append(append(out, `,"icon":`...), JSON(vb.Icon, "")...)
			}
		}
		return append(out, '}'), nil
	}
	return JSON(t, ""), nil
}

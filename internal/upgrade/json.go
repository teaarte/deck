package upgrade

import (
	"bytes"
	"encoding/json"
)

// member is a key of a JSON object with its value as it is written.
type member struct {
	key string
	val json.RawMessage
}

// members are the members of a JSON object in their order.
func members(raw []byte) ([]member, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []member
	for dec.More() {
		k, err := dec.Token()
		key, ok := k.(string)
		if err != nil || !ok {
			return nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, member{key, v})
	}
	return out, true
}

func object(ms []member) json.RawMessage {
	b := []byte{'{'}
	for i, m := range ms {
		if i > 0 {
			b = append(b, ',')
		}
		k, _ := json.Marshal(m.key)
		b = append(append(append(b, k...), ':'), m.val...)
	}
	return append(b, '}')
}

func array(vs []json.RawMessage) json.RawMessage {
	b := []byte{'['}
	for i, v := range vs {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, v...)
	}
	return append(b, ']')
}

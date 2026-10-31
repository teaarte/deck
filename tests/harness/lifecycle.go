package harness

import (
	"encoding/json"
)

func Itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

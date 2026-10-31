package harness

import (
	_ "modernc.org/sqlite"
)

type RecordRow struct {
	ID   int64          `json:"id"`
	Data map[string]any `json:"data"`
}

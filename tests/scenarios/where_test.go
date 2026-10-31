package scenarios

import (
	"deck2/tests/harness"
	"strings"
	"testing"
)

// TestAWhereKeepsTheSameRecordsOnAScreenAndInAList: a where that gives nothing for a record keeps it out in both,
// and one that gives no boolean is an error in both.
func TestAWhereKeepsTheSameRecordsOnAScreenAndInAList(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	where := `topic == 'x' ? true : nil`
	p := e.Project("ideas", strings.Replace(ideasManifest, `"where": "score >= 5"`, `"where": "`+where+`"`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "ideas", "collection": "ideas", "records": harness.Jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": "x"}, map[string]any{"title": "beta", "score": 8, "topic": "y"})}, nil)
	var listed []struct{ Data map[string]any }
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": where}, &listed)
	if rows := e.ScreenRows(p, "main"); len(listed) != 1 || len(rows) != 1 {
		t.Fatalf("both keep only alpha: %+v %+v", listed, rows)
	}
	e.Refused("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": "topic"}, 422, "not true or false")
}

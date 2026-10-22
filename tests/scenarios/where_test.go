package scenarios

import (
	"strings"
	"testing"
)

// TestAWhereKeepsTheSameRecordsOnAScreenAndInAList: a where that gives nothing for a record keeps it out in both,
// and one that gives no boolean is an error in both.
func TestAWhereKeepsTheSameRecordsOnAScreenAndInAList(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	where := `topic == 'x' ? true : nil`
	p := e.createProject("ideas", strings.Replace(ideasManifest, `"where": "score >= 5"`, `"where": "`+where+`"`, 1))
	e.must("deck.records_upsert", map[string]any{"project": "ideas", "collection": "ideas", "records": jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": "x"}, map[string]any{"title": "beta", "score": 8, "topic": "y"})}, nil)
	var listed []struct{ Data map[string]any }
	e.must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": where}, &listed)
	if rows := e.screenRows(p, "main"); len(listed) != 1 || len(rows) != 1 {
		t.Fatalf("both keep only alpha: %+v %+v", listed, rows)
	}
	e.refused("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": "topic"}, 422, "not true or false")
}

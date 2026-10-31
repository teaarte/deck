package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/schemas"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestTheTopicsOfReadsAreTheOnesDeckPublishes: the topics the catalog names are those deck publishes, and a read that
// follows another one keeps deck from starting, so a typo cannot turn off a live screen.
func TestTheTopicsOfReadsAreTheOnesDeckPublishes(t *testing.T) {
	t.Parallel()
	var catalog struct{ Topics []string }
	if err := json.Unmarshal(schemas.Catalog, &catalog); err != nil {
		t.Fatal(err)
	}
	slices.Sort(catalog.Topics)
	if got := store.Topics(); !slices.Equal(got, catalog.Topics) {
		t.Fatalf("the catalog names the topics deck publishes:\n%v\n%v", got, catalog.Topics)
	}
	typo := registry.Read("typo", "Typo", "Follows a topic deck does not publish.", registry.OwnerSide,
		func(context.Context, registry.Actor, struct{}) ([]harness.ProbeRow, error) { return nil, nil }).On("colection")
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "colection") {
			t.Fatalf("a read of a topic deck does not publish keeps deck from starting: %v", p)
		}
	}()
	harness.New(t, harness.WithConfig(func(c *app.Config) { c.Commands = append(c.Commands, typo) }))
}

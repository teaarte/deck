package owner

import (
	"deck2/tests/harness"
	"net/http"
	"strings"
	"testing"
)

// TestATokenInTheAddressOpensOnlyReads: the token in an address, which links and logs may carry, opens the page, its
// events and its files, but no command; a command takes the token in its header.
func TestATokenInTheAddressOpensOnlyReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	r, err := http.Post(e.URL+"/api/commands/deck.projects_list?token="+e.OwnerToken(), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a command with the token only in its address is refused: %d", r.StatusCode)
	}
	g, err := http.Get(e.URL + "/api/events?token=" + e.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	g.Body.Close()
	if g.StatusCode != http.StatusOK {
		t.Fatalf("the events of the page open with the token in the address: %d", g.StatusCode)
	}
}

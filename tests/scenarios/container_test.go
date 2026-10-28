package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"
)

const userScript = pythonLib + `
inp = json.load(sys.stdin)
def readable(p):
    try:
        open(p).read()
        return True
    except OSError:
        return False
data = os.environ["DECK_DATA"]
open(os.path.join(data, "kept.txt"), "w").write("mine")
print(json.dumps({"uid": os.getuid(), "token": readable(inp["secret"]), "base": readable(inp["outside"]),
    "data": open(os.path.join(data, "kept.txt")).read(), "deck": deck("deck.records_list", {"collection": "items"})[0]}))
`

// TestInDecksContainerAScriptRunsAsTheUserOfAgents: in deck's image a script is not deck's user, reads neither deck's
// token nor its base, writes the data of its project and still calls deck.
func TestInDecksContainerAScriptRunsAsTheUserOfAgents(t *testing.T) {
	if !sandbox.HasAgentUser() {
		t.Skip("runs where deck has a user of agents, as in its image: DECK_AGENT_USER")
	}
	e := newEnv(t, nil)
	e.createProject("tools", scriptManifest("collect", "", ""))
	e.saveScript("tools", "scripts/main.py", userScript)
	h := deckhome.Layout(e.App.Cfg.Home)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"secret": h.Token(), "outside": h.DB()}), "done", "error")
	var out struct {
		UID         int
		Token, Base bool
		Data        string
		Deck        int
	}
	if err := json.Unmarshal(p.Result, &out); err != nil || p.State != "done" {
		t.Fatalf("the script runs: %+v", p)
	}
	if out.UID == os.Getuid() || out.Token || out.Base || out.Data != "mine" || out.Deck != 200 {
		t.Fatalf("the script is the user of agents, without deck's token and base, with its data and deck: %+v", out)
	}
	if b, err := os.ReadFile(filepath.Join(h.Data(e.projectID("tools")), "kept.txt")); err != nil || string(b) != "mine" {
		t.Fatalf("deck reads what the script kept: %q %v", b, err)
	}
}

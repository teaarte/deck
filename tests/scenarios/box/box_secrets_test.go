package box

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

const printsSecretsScript = `import base64, json, os, sys
key = os.environ["HTTP_PROXY"].split(":")[2].split("@")[0]
with open(os.path.join(os.environ["DECK_DATA"], "key.txt"), "w") as f:
    f.write(key + "\n" + os.environ["DECK_TOKEN"])
basic = base64.b64encode(("deck:" + key).encode()).decode()
sys.stderr.write("x" * (98304 - 10) + key + "\n")
sys.stderr.write("Proxy-Authorization: Basic " + basic + "\n")
sys.stderr.write("DECK_TOKEN=" + os.environ["DECK_TOKEN"] + "\n")
sys.stderr.flush()
print(json.dumps({"proxy": os.environ["HTTP_PROXY"], "models": os.environ["DECK_MODELS"], "token": os.environ["DECK_TOKEN"], "basic": basic}))
`

// TestAScriptShowsNoKeyOfItsBox: what a script prints of the key of its door, also as Basic credentials, and of its
// token reaches neither its result nor its log.
func TestAScriptShowsNoKeyOfItsBox(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", printsSecretsScript)
	id := e.Start("tools", "collect", nil)
	p := e.WaitState(id, "done", "error")
	var got struct{ Proxy, Models, Token, Basic string }
	_ = json.Unmarshal(p.Result, &got)
	if p.State != "done" || !strings.HasPrefix(got.Proxy, "http://deck:[secret]@") || !strings.Contains(got.Models, "/[secret]/models") ||
		got.Token != "[secret]" || got.Basic != "[secret]" {
		t.Fatalf("the result has the secrets of the box masked: %+v %s", p, p.Result)
	}
	b, err := os.ReadFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Data(pid), "key.txt"))
	key, token, _ := strings.Cut(string(b), "\n")
	if err != nil || len(key) < 16 || len(token) < 16 {
		t.Fatalf("the script had the key and its token: %v %q", err, b)
	}
	secrets := []string{key, token, base64.StdEncoding.EncodeToString([]byte("deck:" + key))}
	stored, err := os.ReadFile(e.App.Runner.LogPath(id))
	if err != nil || strings.Count(string(stored), "[secret]") < 3 {
		t.Fatalf("the log keeps what the script printed, masked: %v %d bytes", err, len(stored))
	}
	journal := fmt.Sprint(e.Log(id))
	for _, s := range secrets {
		if strings.Contains(string(stored), s) || strings.Contains(journal, s) || strings.Contains(string(p.Result), s) {
			t.Fatalf("no secret of the box is kept: %s", s)
		}
	}
}

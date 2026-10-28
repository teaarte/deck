package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// serverOverride adds the model of the test beside deck; compose.yaml itself is as the owner downloads it.
const serverOverride = `services:
  model:
    image: ghcr.io/teaarte/deck:latest
    entrypoint: ["/srv/scenarios.test"]
    environment:
      DECK_FAKE_RESPONSES: ":8080"
    volumes:
      - /srv/scenarios.test:/srv/scenarios.test:ro
`

const serverCodex = `model_provider = "fake"
model = "gpt-5"
[model_providers.fake]
name = "fake"
base_url = "http://model:8080/v1"
wire_api = "responses"
requires_openai_auth = false
`

// TestServerFromScratch is deck on a bare machine, docker in docker: compose.yaml and docker compose up -d, the link
// with the token in the log, the first screen over Caddy's HTTPS, the sign-in links of Claude Code and Codex, Codex on
// a model of the test passing its self-check with a skill, and a module whose script runs as the user of agents.
func TestServerFromScratch(t *testing.T) {
	if os.Getenv("DECK_SERVER") == "" {
		t.Skip("set DECK_SERVER=1 to run deck's image on a bare docker; needs docker with buildx")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("docker", "buildx", "build", "--load", "-t", "ghcr.io/teaarte/deck:latest", root)
	bin := filepath.Join(t.TempDir(), "scenarios.test")
	build := exec.Command("go", "test", "-c", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the test as the model: %v\n%s", err, out)
	}

	name := fmt.Sprintf("deck-server-%d", time.Now().UnixNano())
	run("docker", "run", "-d", "--privileged", "--name", name, "-e", "DOCKER_TLS_CERTDIR=", "docker:dind")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
	in := func(args ...string) string { return run("docker", append([]string{"exec", name}, args...)...) }
	deadline := time.Now().Add(time.Minute)
	for exec.Command("docker", "exec", name, "docker", "info").Run() != nil {
		if time.Now().After(deadline) {
			t.Fatal("docker in docker did not start")
		}
		time.Sleep(time.Second)
	}
	save := exec.Command("sh", "-c", "docker save ghcr.io/teaarte/deck:latest caddy:2 | docker exec -i "+name+" docker load")
	if out, err := save.CombinedOutput(); err != nil {
		t.Fatalf("the images in the bare docker: %v\n%s", err, out)
	}
	in("mkdir", "-p", "/srv/deck")
	run("docker", "cp", filepath.Join(root, "compose.yaml"), name+":/srv/deck/compose.yaml")
	run("docker", "cp", bin, name+":/srv/scenarios.test")
	write := func(path, text string) {
		cmd := exec.Command("docker", "exec", "-i", name, "sh", "-c", "cat > "+path)
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write %s: %v\n%s", path, err, out)
		}
	}
	write("/srv/deck/.env", "DECK_DOMAIN=localhost\n")
	write("/srv/deck/compose.override.yaml", serverOverride)
	compose := func(args ...string) string {
		return in(append([]string{"docker", "compose", "--project-directory", "/srv/deck"}, args...)...)
	}
	compose("up", "-d")

	var token string
	deadline = time.Now().Add(2 * time.Minute)
	for token == "" {
		if m := regexp.MustCompile(`url="https://localhost/\?token=([0-9a-f]+)"`).FindStringSubmatch(compose("logs", "deck")); m != nil {
			token = m[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("no link with the token in the log of deck:\n%s", compose("logs", "deck"))
		}
		time.Sleep(time.Second)
	}
	// Caddy's own certificate for localhost stands for the one it gets for a domain.
	curl := func(args ...string) string {
		return compose(append([]string{"exec", "-T", "deck", "curl", "-sk", "--max-time", "60", "--connect-to", "localhost:443:caddy:443",
			"--connect-to", "localhost:8443:caddy:8443"}, args...)...)
	}
	if page := curl("https://localhost/?token=" + token); !strings.Contains(page, "<html") {
		t.Fatalf("the first screen over HTTPS: %.300s", page)
	}
	if sb := curl("https://localhost:8443/sandbox.html"); !strings.Contains(sb, "localhost") {
		t.Fatalf("the second origin of custom blocks over HTTPS: %.300s", sb)
	}
	call := func(command string, input any) json.RawMessage {
		t.Helper()
		body, _ := json.Marshal(input)
		out := curl("-X", "POST", "-H", "Authorization: Bearer "+token, "-H", "Content-Type: application/json", "-d", string(body),
			"https://localhost/api/commands/"+command)
		return json.RawMessage(out)
	}
	health := func(subject string) (string, string) {
		var rows []struct{ Subject, Status, Error string }
		_ = json.Unmarshal(call("deck.health_list", map[string]any{}), &rows)
		for _, r := range rows {
			if r.Subject == subject {
				return r.Status, r.Error
			}
		}
		return "", ""
	}
	waitHealth := func(subject, want string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		for {
			status, text := health(subject)
			if status == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is %s, want %s: %s", subject, status, want, text)
			}
			time.Sleep(2 * time.Second)
		}
	}
	waitHealth("provider:claude-code", "needs_login")
	waitHealth("provider:codex", "needs_login")
	for _, agent := range []string{"claude-code", "codex"} {
		var l struct{ URL, Code string }
		if err := json.Unmarshal(call("deck.agent_login", map[string]any{"provider": agent}), &l); err != nil || !strings.HasPrefix(l.URL, "https://") {
			t.Fatalf("the sign-in link of %s: %+v %v", agent, l, err)
		}
	}

	cfg := exec.Command("docker", "exec", "-i", name, "docker", "compose", "--project-directory", "/srv/deck", "exec", "-T", "-u", "agent", "deck",
		"sh", "-c", "mkdir -p ~/.codex && cat > ~/.codex/config.toml")
	cfg.Stdin = strings.NewReader(serverCodex)
	if out, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("Codex on the model of the test: %v\n%s", err, out)
	}
	call("deck.provider_check", map[string]any{"provider": "codex"})
	waitHealth("provider:codex", "ok")

	var p struct{ ID int64 }
	if err := json.Unmarshal(call("deck.project_create", map[string]any{"name": "tools", "manifest": scriptManifest("collect", "", "")}), &p); err != nil || p.ID == 0 {
		t.Fatalf("a module: %v", err)
	}
	call("deck.document_save", map[string]any{"project": "tools", "name": "scripts/main.py", "kind": "script", "text": pythonLib + `
print(json.dumps({"uid": os.getuid(), "deck": deck("deck.records_list", {"collection": "items"})[0]}))
`})
	var started struct{ Process int64 }
	_ = json.Unmarshal(call("tools.collect", map[string]any{}), &started)
	deadline = time.Now().Add(2 * time.Minute)
	for {
		var pr struct {
			State  string
			Result json.RawMessage
			Error  string
		}
		_ = json.Unmarshal(call("deck.process_get", map[string]any{"process": started.Process}), &pr)
		if pr.State == "done" {
			var out struct{ UID, Deck int }
			_ = json.Unmarshal(pr.Result, &out)
			deckUID := strings.TrimSpace(compose("exec", "-T", "deck", "id", "-u"))
			if fmt.Sprint(out.UID) == deckUID || out.Deck != 200 {
				t.Fatalf("the script runs as the user of agents and calls deck: %s (deck is %s)", pr.Result, deckUID)
			}
			break
		}
		if pr.State == "error" || time.Now().After(deadline) {
			t.Fatalf("the script of the module: %s %s", pr.State, pr.Error)
		}
		time.Sleep(time.Second)
	}
	if out := compose("logs", "deck"); strings.Contains(out, "panic") {
		t.Fatalf("deck panicked:\n%s", out)
	}
}

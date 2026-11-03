package real

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
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

// serverCodex is Codex on the model of the test, named by its address: a box reaches a local service only so.
const serverCodex = `model_provider = "fake"
model = "gpt-5"
[model_providers.fake]
name = "fake"
base_url = "http://%s:8080/v1"
wire_api = "responses"
requires_openai_auth = false
`

// composeV01 is the compose.yaml of v0.1 as its owners downloaded it, Caddy aside: the logins of the CLIs lie in a
// volume of their own.
const composeV01 = `name: deck
services:
  deck:
    image: ghcr.io/teaarte/deck:%s
    init: true
    restart: unless-stopped
    shm_size: 512m
    environment:
      DECK_PUBLIC: ${DECK_DOMAIN:+https://${DECK_DOMAIN}}
      TZ: ${TZ:-}
    ports:
      - "127.0.0.1:7700:7700"
      - "127.0.0.1:7701:7701"
    volumes:
      - deck:/var/lib/deck
      - agents:/home/agent
volumes:
  deck:
  agents:
`

// pidsScript says how many processes its box shows and whether it reads deck's base, and calls deck.
const pidsScript = harness.PythonLib + `
def readable(p):
    try:
        open(p).read()
        return True
    except OSError:
        return False
pids = [p for p in os.listdir("/proc") if p.isdigit()]
print(json.dumps({"pids": len(pids), "state": readable("/var/lib/deck/state/deck.db"), "deck": deck("deck.records_list", {"collection": "items"})[0]}))
`

// seen is what pidsScript saw.
type seen struct {
	Pids  int
	State bool
	Deck  int
}

// bare is a bare docker, docker in docker, with deck's image as this tree builds it and the test as a model.
type bare struct {
	t    *testing.T
	name string
}

// newBare starts a bare docker with deck's image, Caddy's of compose.yaml and the images named.
func newBare(t *testing.T, images ...string) *bare {
	t.Helper()
	b := &bare{t: t}
	spec, err := os.ReadFile(filepath.Join(harness.Root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	caddy := regexp.MustCompile(`image: (caddy\S*)`).FindSubmatch(spec)
	if caddy == nil {
		t.Fatal("compose.yaml names no image of caddy")
	}
	images = append(images, string(caddy[1]))
	b.host("docker", "buildx", "build", "--load", "-t", "ghcr.io/teaarte/deck:latest", harness.Root)
	bin := filepath.Join(t.TempDir(), "scenarios.test")
	build := exec.Command("go", "test", "-c", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the test as the model: %v\n%s", err, out)
	}
	b.name = fmt.Sprintf("deck-server-%d", time.Now().UnixNano())
	b.host("docker", "run", "-d", "--privileged", "--name", b.name, "-e", "DOCKER_TLS_CERTDIR=", "docker:dind")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", b.name).Run() })
	harness.Until(t, "docker in docker to start", func() bool { return exec.Command("docker", "exec", b.name, "docker", "info").Run() == nil })
	save := exec.Command("sh", "-c", "docker save ghcr.io/teaarte/deck:latest | docker exec -i "+b.name+" docker load")
	if out, err := save.CombinedOutput(); err != nil {
		t.Fatalf("deck's image in the bare docker: %v\n%s", err, out)
	}
	// The bare docker pulls the images of others itself: a save of an image of many platforms leaves layers out.
	for _, img := range images {
		b.in("docker", "pull", "-q", img)
	}
	b.in("mkdir", "-p", "/srv/deck")
	b.host("docker", "cp", bin, b.name+":/srv/scenarios.test")
	b.write("/srv/deck/.env", "DECK_DOMAIN=localhost\n")
	b.write("/srv/deck/compose.override.yaml", serverOverride)
	return b
}

func (b *bare) host(name string, args ...string) string {
	b.t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		b.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (b *bare) in(args ...string) string {
	b.t.Helper()
	return b.host("docker", append([]string{"exec", b.name}, args...)...)
}

// write writes a file of the bare docker; with user it writes one in the container of deck as that user.
func (b *bare) write(path, text string, user ...string) {
	b.t.Helper()
	args := []string{"exec", "-i", b.name, "sh", "-c", "cat > " + path}
	if len(user) > 0 {
		args = []string{"exec", "-i", b.name, "docker", "compose", "--project-directory", "/srv/deck", "exec", "-T", "-u", user[0], "deck",
			"sh", "-c", "mkdir -p " + filepath.Dir(path) + " && cat > " + path}
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.t.Fatalf("write %s: %v\n%s", path, err, out)
	}
}

// files puts files of the repository beside compose.yaml, as the owner downloads them.
func (b *bare) files(names ...string) {
	b.t.Helper()
	for _, n := range names {
		b.host("docker", "cp", filepath.Join(harness.Root, n), b.name+":/srv/deck/"+n)
	}
}

func (b *bare) compose(args ...string) string {
	b.t.Helper()
	return b.in(append([]string{"docker", "compose", "--project-directory", "/srv/deck"}, args...)...)
}

// sh runs a shell in the container of deck as user.
func (b *bare) sh(user, script string) string {
	b.t.Helper()
	return b.compose("exec", "-T", "-u", user, "deck", "sh", "-c", script)
}

// signIn waits for the first link in the log of deck, which has no token, then pairs a browser at deck's https address
// by a code of docker compose exec deck deck pair inside the container and gives the cookie of its session.
func (b *bare) signIn() string {
	b.t.Helper()
	var logs string
	first := regexp.MustCompile(`link="?https://localhost/#pair=[A-Za-z0-9_-]+`)
	defer func() {
		if !first.MatchString(logs) {
			b.t.Logf("the log of deck:\n%s", logs)
		}
	}()
	harness.Until(b.t, "the link of the first device in the log of deck", func() bool {
		logs = b.compose("logs", "deck")
		return first.MatchString(logs)
	})
	if strings.Contains(logs, "token=") {
		b.t.Fatalf("the log of deck carries no token:\n%s", logs)
	}
	out := b.compose("exec", "-T", "-u", "deck", "deck", "deck", "pair")
	m := regexp.MustCompile(`https://localhost/#pair=([A-Za-z0-9_-]+)`).FindStringSubmatch(out)
	if m == nil {
		b.t.Fatalf("deck pair prints a link of deck's https address: %s", out)
	}
	cookie, err := harness.NewPasskey().PairBy(func(step string, body any) (int, []byte, string, error) {
		in, _ := json.Marshal(body)
		out := b.compose("exec", "-T", "-u", "deck", "deck", "curl", "-s", "-i", "--max-time", "60", "-X", "POST", "-H", "Host: localhost",
			"-H", "Origin: "+origin, "-H", "Content-Type: application/json", "-d", string(in), "http://127.0.0.1:7700/auth/"+step)
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(out)), nil)
		if err != nil {
			return 0, []byte(out), "", err
		}
		return harness.Answer(resp)
	}, origin, m[1], "server test")
	if err != nil {
		b.t.Fatal(err)
	}
	return cookie
}

// origin is deck's https address in the test, by Caddy of its compose.
const origin = "https://localhost"

// call calls a command of deck from inside its container, as the owner's browser signed in at its https address.
func (b *bare) call(cookie, command string, input any) json.RawMessage {
	b.t.Helper()
	body, _ := json.Marshal(input)
	return json.RawMessage(b.compose("exec", "-T", "-u", "deck", "deck", "curl", "-s", "--max-time", "60", "-X", "POST",
		"-b", "__Host-deck="+cookie, "-H", "Host: localhost", "-H", "Origin: "+origin, "-H", "Content-Type: application/json", "-d", string(body),
		"http://127.0.0.1:7700/api/commands/"+command))
}

// health waits for the row of subject to be want and gives its text.
func (b *bare) health(token, subject, want string) string {
	b.t.Helper()
	var status, text string
	defer func() {
		if status != want {
			b.t.Logf("%s is %s, want %s: %s", subject, status, want, text)
		}
	}()
	harness.UntilWithin(b.t, 4*time.Minute, subject+" to be "+want, func() bool {
		var rows []struct{ Subject, Status, Error string }
		_ = json.Unmarshal(b.call(token, "deck.health_list", map[string]any{}), &rows)
		for _, r := range rows {
			if r.Subject == subject {
				status, text = r.Status, r.Error
			}
		}
		return status == want
	})
	return text
}

// script runs the script of the module tools, made the first time, and gives how its process ended.
func (b *bare) script(token string) (state, errText string, out seen) {
	b.t.Helper()
	b.call(token, "deck.project_create", map[string]any{"name": "tools", "manifest": harness.ScriptManifest("collect", "", "")})
	b.call(token, "deck.document_save", map[string]any{"project": "tools", "name": "scripts/main.py", "kind": "script", "text": pidsScript})
	var started struct{ Process int64 }
	_ = json.Unmarshal(b.call(token, "tools.collect", map[string]any{}), &started)
	harness.Until(b.t, "the script of the module to end", func() bool {
		var pr struct {
			State  string
			Result json.RawMessage
			Error  string
		}
		_ = json.Unmarshal(b.call(token, "deck.process_get", map[string]any{"process": started.Process}), &pr)
		state, errText = pr.State, pr.Error
		_ = json.Unmarshal(pr.Result, &out)
		return state == "done" || state == "error"
	})
	return state, errText, out
}

// codex points Codex at the model of the test, in its folder as user.
func (b *bare) codex(user, folder string) {
	b.t.Helper()
	ip := strings.Fields(b.sh(user, "getent hosts model"))
	if len(ip) == 0 {
		b.t.Fatal("the model of the test has no address")
	}
	b.write(folder+"/config.toml", fmt.Sprintf(serverCodex, ip[0]), user)
}

// TestServerFromScratch is deck from compose.yaml with its three lines: a link of deck pair, no token in the log, HTTPS
// by Caddy, sign-in links, Codex passing its self-check, a script in its box that sees no other process nor deck's base.
func TestServerFromScratch(t *testing.T) {
	harness.Needs(t, "DECK_SERVER")
	b := newBare(t)
	b.files("compose.yaml", "seccomp.json", "deck-box.apparmor")
	b.compose("up", "-d")
	token := b.signIn()

	// Caddy's own certificate for localhost stands for the one it gets for a domain.
	curl := func(args ...string) string {
		return b.compose(append([]string{"exec", "-T", "deck", "curl", "-sk", "--max-time", "60", "--connect-to", "localhost:443:caddy:443",
			"--connect-to", "localhost:8443:caddy:8443"}, args...)...)
	}
	if page := curl("https://localhost/"); !strings.Contains(page, "<html") {
		t.Fatalf("the first screen over HTTPS: %.300s", page)
	}
	if sb := curl("https://localhost:8443/sandbox.html"); !strings.Contains(sb, "localhost") {
		t.Fatalf("the second origin of custom blocks over HTTPS: %.300s", sb)
	}
	b.health(token, "sandbox", "ok")
	b.health(token, "provider:claude-code", "needs_login")
	b.health(token, "provider:codex", "needs_login")
	for _, agent := range []string{"claude-code", "codex"} {
		var l struct{ URL, Code string }
		if err := json.Unmarshal(b.call(token, "deck.agent_login", map[string]any{"provider": agent}), &l); err != nil || !strings.HasPrefix(l.URL, "https://") {
			t.Fatalf("the sign-in link of %s: %+v %v", agent, l, err)
		}
	}

	b.codex("deck", "/var/lib/deck/agents/codex/login")
	b.call(token, "deck.provider_check", map[string]any{"provider": "codex"})
	b.health(token, "provider:codex", "ok")

	if state, errText, out := b.script(token); state != "done" || out.Pids == 0 || out.Pids > 5 || out.State || out.Deck != 200 {
		t.Fatalf("the script runs in its box, sees only its own processes and not deck's base, and calls deck: %s %s %+v", state, errText, out)
	}
	if logs := b.compose("logs", "deck"); strings.Contains(logs, "panic") {
		t.Fatalf("deck panicked:\n%s", logs)
	}
}

// TestServerTakesOverTheVolumesOfV01: under the compose.yaml of v0.1 the image copies the logins of its user agent into
// deck's folder and refuses foreign programs, saying the lines it lacks; under this compose.yaml the CLIs find them.
func TestServerTakesOverTheVolumesOfV01(t *testing.T) {
	harness.Needs(t, "DECK_SERVER")
	b := newBare(t, "ghcr.io/teaarte/deck:v0.1.1")
	b.write("/srv/deck/compose.yaml", fmt.Sprintf(composeV01, "v0.1.1"))
	b.compose("up", "-d")
	b.sh("root", `set -e
mkdir -p /home/agent/.claude /home/agent/.codex /var/lib/deck/work/left
echo '{"claudeAiOauth":{"accessToken":"sk-ant-oat01-old","refreshToken":"sk-ant-ort01-old","expiresAt":4102444800000}}' > /home/agent/.claude/.credentials.json
echo '{"OPENAI_API_KEY":"sk-old"}' > /home/agent/.codex/auth.json
echo left > /var/lib/deck/work/left/file
chown -R agent:agent /home/agent /var/lib/deck/work/left`)
	b.codex("agent", "/home/agent/.codex")

	b.write("/srv/deck/compose.yaml", fmt.Sprintf(composeV01, "latest"))
	b.compose("up", "-d")
	token := b.signIn()
	fix := b.health(token, "sandbox", "error")
	for _, line := range []string{"seccomp=./seccomp.json", "systempaths=unconfined", "apparmor=deck-box", "apparmor_parser -r /etc/apparmor.d/deck-box"} {
		if !strings.Contains(fix, line) {
			t.Fatalf("without its lines health says what to fix, %s among them: %s", line, fix)
		}
	}
	if state, errText, _ := b.script(token); state != "error" || !strings.Contains(errText, "without its box") {
		t.Fatalf("no script runs without its box: %s %s", state, errText)
	}
	moved := b.sh("deck", `cd /var/lib/deck && cat agents/claude/login/.credentials.json agents/codex/login/auth.json agents/codex/login/config.toml &&
stat -c '%U %n' work/left/file agents/claude/login/.credentials.json agents/codex/login/auth.json`)
	for _, want := range []string{"sk-ant-ort01-old", "sk-old", "base_url", "deck work/left/file",
		"deck agents/claude/login/.credentials.json", "deck agents/codex/login/auth.json"} {
		if !strings.Contains(moved, want) {
			t.Fatalf("the logins of v0.1 are deck's in the folders of their CLIs, and so is what its agents left (%s):\n%s", want, moved)
		}
	}

	b.files("compose.yaml", "seccomp.json", "deck-box.apparmor")
	b.compose("up", "-d")
	b.health(token, "sandbox", "ok")
	b.call(token, "deck.provider_check", map[string]any{"provider": "codex"})
	b.health(token, "provider:codex", "ok")
	if state, errText, out := b.script(token); state != "done" || out.State || out.Deck != 200 {
		t.Fatalf("with its lines the script runs in its box: %s %s %+v", state, errText, out)
	}
	if logs := b.compose("logs", "deck"); strings.Contains(logs, "panic") {
		t.Fatalf("deck panicked:\n%s", logs)
	}
}

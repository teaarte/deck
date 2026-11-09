package real

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/manifest"
	"deck2/tests/harness"
)

// serverOverride adds the model of the test beside deck; compose.yaml itself is as the owner downloads it. Not of
// latest: an update of deck leaves the model, and its address Codex is pointed at, as they were.
const serverOverride = `services:
  model:
    image: ghcr.io/teaarte/deck:candidate
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
	// origin is the address of deck its owner signs in at: https by Caddy with a domain, deck.localhost without.
	origin string
	// caddy is the image of Caddy compose.yaml names.
	caddy string
	// ctr is the container of a deck run as a binary, not by compose; exe its binary there and home its folder.
	ctr, exe, home string
	// pairAs is the user deck pair runs as, root's as README runs it when empty.
	pairAs string
}

// candidate is deck's image and the test as a model, built once for every bare docker of the run.
var candidate struct {
	once       sync.Once
	image, bin string
	err        error
}

// build builds deck's image from this tree and the test as the model, once.
func build(t *testing.T) (image, bin string) {
	t.Helper()
	candidate.once.Do(func() {
		// One folder a tree, written over by each run: no run leaves one behind.
		dir := filepath.Join(os.TempDir(), fmt.Sprintf("deck-server-%x", sha256.Sum256([]byte(harness.Root)))[:8+len("deck-server-")])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			candidate.err = err
			return
		}
		iid := filepath.Join(dir, "iid")
		// By its id: another test of this machine may tag an image of its own tree latest meanwhile.
		if out, err := exec.Command("docker", "buildx", "build", "--load", "--iidfile", iid, "-t", "ghcr.io/teaarte/deck:latest", harness.Root).CombinedOutput(); err != nil {
			candidate.err = fmt.Errorf("deck's image: %v\n%s", err, out)
			return
		}
		id, err := os.ReadFile(iid)
		if err != nil {
			candidate.err = err
			return
		}
		candidate.image = strings.TrimSpace(string(id))
		candidate.bin = filepath.Join(dir, "scenarios.test")
		build := exec.Command("go", "test", "-c", "-o", candidate.bin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := build.CombinedOutput(); err != nil {
			candidate.err = fmt.Errorf("the test as the model: %v\n%s", err, out)
		}
	})
	if candidate.err != nil {
		t.Fatal(candidate.err)
	}
	return candidate.image, candidate.bin
}

// newBare starts a bare docker with deck's image as latest and as candidate, Caddy's of compose.yaml and the images
// named.
func newBare(t *testing.T, images ...string) *bare {
	t.Helper()
	b := &bare{t: t, origin: "https://localhost", exe: "deck", home: "/var/lib/deck"}
	spec, err := os.ReadFile(filepath.Join(harness.Root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	caddy := regexp.MustCompile(`image: (caddy\S*)`).FindSubmatch(spec)
	if caddy == nil {
		t.Fatal("compose.yaml names no image of caddy")
	}
	b.caddy = string(caddy[1])
	images = append(images, b.caddy)
	image, bin := build(t)
	b.name = fmt.Sprintf("deck-server-%d", time.Now().UnixNano())
	b.host("docker", "run", "-d", "--privileged", "--name", b.name, "-e", "DOCKER_TLS_CERTDIR=", "docker:dind")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", b.name).Run() })
	harness.Until(t, "docker in docker to start", func() bool { return exec.Command("docker", "exec", b.name, "docker", "info").Run() == nil })
	out, err := exec.Command("sh", "-c", "docker save "+image+" | docker exec -i "+b.name+" docker load").CombinedOutput()
	loaded := regexp.MustCompile(`Loaded image(?: ID)?: (\S+)`).FindSubmatch(out)
	if err != nil || loaded == nil {
		t.Fatalf("deck's image in the bare docker: %v\n%s", err, out)
	}
	b.in("docker", "tag", string(loaded[1]), "ghcr.io/teaarte/deck:latest")
	b.in("docker", "tag", string(loaded[1]), "ghcr.io/teaarte/deck:candidate")
	// The bare docker pulls the images of others itself: a save of an image of many platforms leaves layers out.
	for _, img := range images {
		b.in("docker", "pull", "-q", img)
	}
	b.in("mkdir", "-p", "/srv/deck")
	b.host("docker", "cp", bin, b.name+":/srv/scenarios.test")
	b.write("/srv/deck/.env", "DECK_DOMAIN=localhost\nCOMPOSE_PROFILES=domain\n")
	b.write("/srv/deck/compose.override.yaml", serverOverride)
	return b
}

// as is the bare docker in the test t.
func (b *bare) as(t *testing.T) *bare {
	c := *b
	c.t = t
	return &c
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

// deck is the command that runs args in the container of deck as user, its own user when empty.
func (b *bare) deck(user string, args ...string) *exec.Cmd {
	cmd := []string{"exec", "-i", b.name, "docker", "compose", "--project-directory", "/srv/deck", "exec", "-T"}
	if b.ctr != "" {
		cmd = []string{"exec", "-i", b.name, "docker", "exec", "-i"}
	}
	if user != "" {
		cmd = append(cmd, "-u", user)
	}
	return exec.Command("docker", append(append(cmd, cmp.Or(b.ctr, "deck")), args...)...)
}

// write writes a file of the bare docker; with user it writes one in the container of deck as that user.
func (b *bare) write(path, text string, user ...string) {
	b.t.Helper()
	cmd := exec.Command("docker", "exec", "-i", b.name, "sh", "-c", "cat > "+path)
	if len(user) > 0 {
		cmd = b.deck(user[0], "sh", "-c", "mkdir -p "+filepath.Dir(path)+" && cat > "+path)
	}
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
	out, err := b.deck(user, "sh", "-c", script).CombinedOutput()
	if err != nil {
		b.t.Fatalf("%s: %v\n%s", script, err, out)
	}
	return string(out)
}

// logs is the log of deck.
func (b *bare) logs() string {
	b.t.Helper()
	if b.ctr != "" {
		return b.in("docker", "logs", b.ctr)
	}
	return b.compose("logs", "deck")
}

// signIn waits for the first link in the log of deck, which has no token, then pairs a browser by deck pair.
func (b *bare) signIn() string {
	b.t.Helper()
	var logs string
	first := regexp.MustCompile(`link="?` + regexp.QuoteMeta(b.origin) + `/#pair=[A-Za-z0-9_-]+`)
	defer func() {
		if !first.MatchString(logs) {
			b.t.Logf("the log of deck:\n%s", logs)
		}
	}()
	harness.Until(b.t, "the link of the first device in the log of deck", func() bool {
		logs = b.logs()
		return first.MatchString(logs)
	})
	if strings.Contains(logs, "token=") {
		b.t.Fatalf("the log of deck carries no token:\n%s", logs)
	}
	return b.pair()
}

// pair pairs a browser at deck's origin by a code of deck pair inside the container of deck and gives the cookie of
// its session.
func (b *bare) pair() string {
	b.t.Helper()
	// As README runs it: as root, which writes the code as the user of deck.
	out, err := b.deck(b.pairAs, b.exe, "pair").CombinedOutput()
	m := regexp.MustCompile(regexp.QuoteMeta(b.origin) + `/#pair=([A-Za-z0-9_-]+)`).FindStringSubmatch(string(out))
	if err != nil || m == nil {
		b.t.Fatalf("deck pair prints a link of %s: %v %s", b.origin, err, out)
	}
	cookie, err := harness.NewPasskey().PairBy(func(step string, body any) (int, []byte, string, error) {
		in, _ := json.Marshal(body)
		out, err := b.deck("deck", "curl", "-s", "-i", "--max-time", "60", "-X", "POST", "-H", "Host: "+b.originHost(),
			"-H", "Origin: "+b.origin, "-H", "Content-Type: application/json", "-d", string(in), "http://127.0.0.1:7700/auth/"+step).Output()
		if err != nil {
			return 0, out, "", err
		}
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(out))), nil)
		if err != nil {
			return 0, out, "", err
		}
		return harness.Answer(resp)
	}, b.origin, m[1], "server test")
	if err != nil {
		b.t.Fatal(err)
	}
	return cookie
}

// originHost is the Host of deck's origin.
func (b *bare) originHost() string {
	return strings.TrimPrefix(strings.TrimPrefix(b.origin, "https://"), "http://")
}

// try calls a command of deck from inside its container, as the owner's browser signed in at deck's origin, or with
// the owner's token of v0.1 when cookie is an Authorization header; false when deck does not answer.
func (b *bare) try(cookie, command string, input any) (json.RawMessage, bool) {
	b.t.Helper()
	// deck's cookie at deck.localhost is named by its port.
	name := "deck-" + b.origin[strings.LastIndex(b.origin, ":")+1:]
	if strings.HasPrefix(b.origin, "https://") {
		name = "__Host-deck"
	}
	auth := []string{"-b", name + "=" + cookie}
	if strings.HasPrefix(cookie, "Authorization: ") {
		auth = []string{"-H", cookie}
	}
	body, _ := json.Marshal(input)
	args := append([]string{"curl", "-s", "--fail-with-body", "--max-time", "60", "-X", "POST"}, auth...)
	out, err := b.deck("deck", append(args, "-H", "Host: "+b.originHost(), "-H", "Origin: "+b.origin, "-H", "Content-Type: application/json",
		"-d", string(body), "http://127.0.0.1:7700/api/commands/"+command)...).Output()
	return json.RawMessage(out), err == nil
}

// call calls a command of deck as try does and gives its answer, an error too.
func (b *bare) call(cookie, command string, input any) json.RawMessage {
	b.t.Helper()
	out, _ := b.try(cookie, command, input)
	return out
}

// must calls a command of deck that should answer.
func (b *bare) must(cookie, command string, input any) json.RawMessage {
	b.t.Helper()
	out, ok := b.try(cookie, command, input)
	if !ok {
		b.t.Fatalf("%s: %s", command, out)
	}
	return out
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

// imagePrograms are the programs deck's image carries for modules: those the skill of modules lists, those the
// manifests of deck's own modules and starters name, their interpreters, and the CLIs of the agents deck checks.
func imagePrograms(t *testing.T) []string {
	t.Helper()
	skill, err := os.ReadFile(filepath.Join(harness.Root, "skills", "module-builder", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile("deck's image carries the programs([^;]*);").FindSubmatch(skill)
	if line == nil {
		t.Fatal("the skill of modules lists the programs of deck's image")
	}
	progs := []string{"claude", "codex", "goose"}
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllSubmatch(line[1], -1) {
		progs = append(progs, string(m[1]))
	}
	manifests, _ := filepath.Glob(filepath.Join(harness.Root, "starters", "*", "manifest.json"))
	for _, path := range append(manifests, filepath.Join(harness.Root, "master", "manifest.json")) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Scripts []manifest.Script }
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, s := range m.Scripts {
			if p := map[string]string{"python": "python3", "node": "node", "bash": "bash"}[s.Runtime]; p != "" {
				progs = append(progs, p)
			}
			for _, p := range s.Programs {
				progs = append(progs, p.Name)
			}
		}
	}
	slices.Sort(progs)
	return slices.Compact(progs)
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
	progs := imagePrograms(t)
	var has, missing []string
	for _, p := range progs {
		if strings.TrimSpace(b.sh("deck", "command -v "+p+" || true")) == "" {
			missing = append(missing, p)
		} else {
			has = append(has, p)
		}
	}
	t.Logf("deck's image has: %s", strings.Join(has, " "))
	if len(missing) > 0 {
		t.Fatalf("deck's image lacks programs modules name: %s", strings.Join(missing, " "))
	}
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

// TestServerOnOneComputer is deck installed by install.sh with no domain: no Caddy and no port 80 or 443, deck on
// loopback, a link at deck.localhost, a browser pairing and calling there, a script in its box.
func TestServerOnOneComputer(t *testing.T) {
	harness.Needs(t, "DECK_SERVER")
	b := newBare(t)
	b.origin = "http://deck.localhost:7700"
	b.write("/srv/deck/.env", "")
	b.in("mkdir", "-p", "/srv/src")
	for _, n := range []string{"install.sh", "compose.yaml", "seccomp.json", "deck-box.apparmor", "move-to-docker.sh"} {
		b.host("docker", "cp", filepath.Join(harness.Root, n), b.name+":/srv/src/"+n)
	}
	if out := b.in("env", "DECK_SOURCE=/srv/src", "DECK_DIR=/srv/deck", "sh", "/srv/src/install.sh"); !strings.Contains(out, "http://deck.localhost:7700/#pair=") {
		t.Fatalf("install.sh starts deck and prints a link that pairs a device:\n%s", out)
	}
	if running := b.compose("ps", "--services", "--status", "running"); strings.Contains(running, "caddy") || !strings.Contains(running, "deck") {
		t.Fatalf("without a domain only deck runs: %s", running)
	}
	if ports := b.in("docker", "ps", "--format", "{{.Ports}}"); strings.Contains(ports, ":80->") || strings.Contains(ports, ":443->") ||
		!strings.Contains(ports, "127.0.0.1:7700") {
		t.Fatalf("deck takes 7700 and 7701 on loopback, nothing 80 or 443: %s", ports)
	}
	// Again with no DECK_DIR and no terminal, as an agent runs it: the deck that runs stays as it was.
	if out, err := exec.Command("docker", "exec", b.name, "env", "HOME=/srv", "DECK_SOURCE=/srv/src", "sh", "/srv/src/install.sh").CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "no terminal to ask") {
		t.Fatalf("install.sh with no DECK_DIR leaves a running deck to a yes: %v\n%s", err, out)
	}
	token := b.signIn()
	harness.Until(t, "the first screen by deck.localhost on the published port", func() bool {
		out, err := exec.Command("docker", "exec", b.name, "wget", "-qO-", "--header", "Host: deck.localhost:7700", "http://127.0.0.1:7700/").CombinedOutput()
		return err == nil && strings.Contains(string(out), "<html")
	})
	b.health(token, "sandbox", "ok")
	if state, errText, out := b.script(token); state != "done" || out.State || out.Deck != 200 {
		t.Fatalf("the script runs in its box: %s %s %+v", state, errText, out)
	}
	if logs := b.compose("logs", "deck"); strings.Contains(logs, "panic") {
		t.Fatalf("deck panicked:\n%s", logs)
	}
}

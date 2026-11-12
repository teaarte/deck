package real

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/home"
	"deck2/internal/release"
	"deck2/tests/harness"
)

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

// releases are the released versions of deck, oldest first; an owner updates from any of them.
var releases = []string{"v0.1.1", "v0.2.0", "v0.2.1", "v0.2.2"}

// candidateVersion is the version the binary of this tree is released as in the test, newer than every release.
const candidateVersion = "v0.3.0"

// keptManifest is a module with records and an HTTP tool that names a secret.
var keptManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}}],
  "tools": {"http": [{"name": "get", "description": "Gets an item.", "method": "GET", "url": "https://example.com/item",
    "headers": {"X-Key": "$secret.key"}}]}}`)

// What an owner keeps in deck across an update.
const (
	keptSecret  = "sk-kept-across-updates"
	keptText    = "What the owner wrote before the update."
	claudeLogin = `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-old","refreshToken":"sk-ant-ort01-old","expiresAt":4102444800000}}`
	codexLogin  = `{"OPENAI_API_KEY":"sk-old"}`
)

// releaseIP is the address of github.com in the test: a public one, bound on the loopback of the test's docker.
const releaseIP = "1.2.3.4"

// TestServerUpdatesFromEveryRelease: a deck of each release with a module, its records, a secret, a document, the
// logins of the CLIs and a paired device takes the image of this tree, or its binary over the air, and keeps them.
func TestServerUpdatesFromEveryRelease(t *testing.T) {
	harness.Needs(t, "DECK_SERVER")
	var images []string
	for _, v := range releases {
		images = append(images, "ghcr.io/teaarte/deck:"+v)
	}
	b := newBare(t, images...)
	t.Run(releases[0], func(t *testing.T) { b.as(t).fromV01() })
	for _, v := range releases[1:] {
		t.Run(v, func(t *testing.T) { b.as(t).fromImage(v) })
	}
	last := releases[len(releases)-1]
	t.Run("binary "+last, func(t *testing.T) { b.as(t).fromBinary(last) })
}

// fresh leaves the bare docker with no deck, no volume and latest the image of this tree.
func (b *bare) fresh() {
	b.t.Helper()
	b.in("sh", "-c", `docker ps -aq | xargs -r docker rm -f -v >/dev/null && docker volume prune -af >/dev/null &&
rm -f /srv/deck/compose.yaml /srv/deck/seccomp.json /srv/deck/deck-box.apparmor &&
docker tag ghcr.io/teaarte/deck:candidate ghcr.io/teaarte/deck:latest`)
}

// fromV01: under the compose.yaml of v0.1 the image copies the logins of its user agent into deck's folder and
// refuses foreign programs, saying the lines it lacks; under this compose.yaml the CLIs find them.
func (b *bare) fromV01() {
	t := b.t
	t.Logf("update from %s", releases[0])
	b.fresh()
	b.write("/srv/deck/compose.yaml", fmt.Sprintf(composeV01, releases[0]))
	b.compose("up", "-d")
	b.keep(harness.AuthorizationHeader(b.token()), releases[0])
	b.sh("root", `set -e
mkdir -p /home/agent/.claude /home/agent/.codex /var/lib/deck/work/left
cat > /home/agent/.claude/.credentials.json <<'EOF'
`+claudeLogin+`
EOF
cat > /home/agent/.codex/auth.json <<'EOF'
`+codexLogin+`
EOF
echo left > /var/lib/deck/work/left/file
chown -R agent:agent /home/agent /var/lib/deck/work/left`)
	b.codex("agent", "/home/agent/.codex")

	b.write("/srv/deck/compose.yaml", fmt.Sprintf(composeV01, "latest"))
	b.compose("up", "-d")
	cookie := b.signIn()
	fix := b.health(cookie, "sandbox", "error")
	for _, line := range []string{"seccomp=./seccomp.json", "systempaths=unconfined", "apparmor=deck-box", "apparmor_parser -r /etc/apparmor.d/deck-box"} {
		if !strings.Contains(fix, line) {
			t.Fatalf("without its lines health says what to fix, %s among them: %s", line, fix)
		}
	}
	if state, errText, _ := b.script(cookie); state != "error" || !strings.Contains(errText, "without its box") {
		t.Fatalf("no script runs without its box: %s %s", state, errText)
	}
	moved := b.sh("deck", `cd /var/lib/deck && cat agents/codex/login/config.toml && stat -c '%U %n' work/left/file agents/claude/login/.credentials.json agents/codex/login/auth.json`)
	for _, want := range []string{"base_url", "deck work/left/file", "deck agents/claude/login/.credentials.json", "deck agents/codex/login/auth.json"} {
		if !strings.Contains(moved, want) {
			t.Fatalf("the logins of v0.1 are deck's in the folders of their CLIs, and so is what its agents left (%s):\n%s", want, moved)
		}
	}
	b.kept(cookie)

	b.files("compose.yaml", "seccomp.json", "deck-box.apparmor")
	b.compose("up", "-d")
	b.health(cookie, "sandbox", "ok")
	b.call(cookie, "deck.provider_check", map[string]any{"provider": "codex"})
	b.health(cookie, "provider:codex", "ok")
	if state, errText, out := b.script(cookie); state != "done" || out.State || out.Deck != 200 {
		t.Fatalf("with its lines the script runs in its box: %s %s %+v", state, errText, out)
	}
	b.devices(cookie)
	b.calm()
}

// rollback goes back to the image of v by the steps of docs/install.md, "Restoring a snapshot": the base of the
// snapshot the update kept, then the older image, which finds what it kept.
func (b *bare) rollback(cookie, v string) {
	t := b.t
	t.Helper()
	b.compose("stop", "deck")
	run := []string{"run", "--rm", "--no-deps", "--user", "deck", "--entrypoint"}
	var snap string
	for _, f := range strings.Fields(b.compose(append(run, "ls", "deck", "/var/lib/deck/state/backups")...)) {
		if strings.HasPrefix(f, "update-") && strings.HasSuffix(f, ".tar.gz") {
			snap = max(snap, f)
		}
	}
	if snap == "" {
		t.Fatal("the update kept the base before it in backups/update-<time>.tar.gz")
	}
	b.compose(append(run, "sh", "deck", "-c", "cd /var/lib/deck && rm -f state/deck.db state/deck.db-wal state/deck.db-shm && tar -xzf state/backups/"+snap+" state/deck.db")...)
	b.write("/srv/deck/compose.override.yaml", "services:\n  deck:\n    image: ghcr.io/teaarte/deck:"+v+"\n")
	defer b.in("rm", "-f", "/srv/deck/compose.override.yaml")
	b.compose("up", "-d")
	var ver struct{ Version string }
	harness.UntilWithin(t, 4*time.Minute, "deck of "+v+" to answer again on the base it had", func() bool {
		out, ok := b.try(cookie, "deck.version_get", map[string]any{})
		return ok && json.Unmarshal(out, &ver) == nil && ver.Version == v
	})
	b.kept(cookie)
	b.compose("stop", "deck")
}

// fromImage: a deck of release v, from compose.yaml and its two files of v as its owners downloaded them, takes the
// image of this tree as docker compose pull brings it.
func (b *bare) fromImage(v string) {
	b.t.Logf("update from %s", v)
	b.fresh()
	for _, f := range []string{"compose.yaml", "seccomp.json", "deck-box.apparmor"} {
		b.in("wget", "-q", "-O", "/srv/deck/"+f, "https://raw.githubusercontent.com/teaarte/deck/"+v+"/"+f)
	}
	b.in("docker", "tag", "ghcr.io/teaarte/deck:"+v, "ghcr.io/teaarte/deck:latest")
	b.compose("up", "-d")
	if v == "v0.2.0" {
		// deck pair of v0.2.0 as root wrote a code its deck did not read; its owners ran it as deck.
		b.pairAs = "deck"
	}
	cookie := b.signIn()
	b.pairAs = ""
	b.keep(cookie, v)
	b.logins()
	b.codex("deck", home.Layout(b.home).Login("codex"))

	b.in("docker", "tag", "ghcr.io/teaarte/deck:candidate", "ghcr.io/teaarte/deck:latest")
	b.compose("up", "-d")
	b.updated(cookie, v)
	b.kept(cookie)
	b.health(cookie, "sandbox", "ok")
	b.call(cookie, "deck.provider_check", map[string]any{"provider": "codex"})
	b.health(cookie, "provider:codex", "ok")
	if state, errText, out := b.script(cookie); state != "done" || out.State || out.Deck != 200 {
		b.t.Fatalf("the script runs in its box: %s %s %+v", state, errText, out)
	}
	b.devices(cookie)
	b.calm()
	if v == releases[len(releases)-1] {
		b.rollback(cookie, v)
	}
}

// fromBinary: the binary of release v as GitHub keeps it, the key it trusts aside, finds the binary of this tree in a
// signed release at github.com, puts the one of its own variant in place and restarts in it on the same folder.
func (b *bare) fromBinary(v string) {
	t := b.t
	t.Logf("update from %s, its binary over the air", v)
	b.fresh()
	variant := "deck-linux-" + runtime.GOARCH + "-tailnet"
	dir := t.TempDir()
	b.in("mkdir", "-p", "/srv/rel/www/teaarte/deck/releases/latest/download", "/srv/bin", "/srv/home")
	got := map[string][]byte{}
	for _, f := range []string{variant, release.List, release.Signature} {
		b.in("wget", "-q", "-O", "/srv/rel/"+f, "https://github.com/teaarte/deck/releases/download/"+v+"/"+f)
		b.host("docker", "cp", b.name+":/srv/rel/"+f, filepath.Join(dir, f))
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		got[f] = raw
	}
	list, err := release.Read(release.Key(), got[release.List], got[release.Signature])
	if err != nil || list.Version != v {
		t.Fatalf("the signed list of %s: %+v %v", v, list, err)
	}
	if f, ok := list.File(variant); !ok || f.SHA256 != sum(got[variant]) {
		t.Fatalf("%s of %s is the one of its signed list: %+v", variant, v, f)
	}
	// The one change to the released binary: it trusts the key of the test, the key of releases being a secret.
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	theirs, ours := []byte(base64.StdEncoding.EncodeToString(release.Key())), []byte(base64.StdEncoding.EncodeToString(pub))
	if n := bytes.Count(got[variant], theirs); n != 1 {
		t.Fatalf("the binary of %s holds the key of releases once, not %d times", v, n)
	}
	b.put("/srv/bin/deck", bytes.Replace(got[variant], theirs, ours, 1))

	files := release.Files{Version: candidateVersion}
	built := map[string]string{}
	for _, tags := range []string{"", "tailnet"} {
		name := strings.TrimSuffix("deck-linux-"+runtime.GOARCH+"-"+tags, "-")
		out := filepath.Join(dir, name)
		// As release.yml builds it.
		cmd := exec.Command("go", "build", "-tags", tags, "-ldflags", "-s -w -X deck2/internal/release.Version="+candidateVersion, "-o", out, "./cmd/deck")
		cmd.Dir = harness.Root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, o)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		built[name] = sum(raw)
		files.Files = append(files.Files, release.File{Name: name, Size: int64(len(raw)), SHA256: built[name]})
		b.put("/srv/rel/www/teaarte/deck/releases/latest/download/"+name, raw)
	}
	signed, sig, err := release.Sign(key, files)
	if err != nil {
		t.Fatal(err)
	}
	b.put("/srv/rel/www/teaarte/deck/releases/latest/download/"+release.List, signed)
	b.put("/srv/rel/www/teaarte/deck/releases/latest/download/"+release.Signature, sig)

	cert, certKey := selfSigned(t, "github.com")
	b.write("/srv/rel/cert.pem", cert)
	b.write("/srv/rel/key.pem", certKey)
	b.write("/srv/rel/Caddyfile", "{\n\tadmin off\n\tauto_https off\n}\nhttps://github.com:443 {\n\tbind "+releaseIP+
		"\n\ttls /srv/rel/cert.pem /srv/rel/key.pem\n\troot * /srv/rel/www\n\tfile_server\n}\n")
	b.in("ip", "addr", "add", releaseIP+"/32", "dev", "lo")
	b.in("docker", "run", "-d", "--name", "github", "--network", "host", "-v", "/srv/rel:/srv/rel:ro", b.caddy,
		"caddy", "run", "--config", "/srv/rel/Caddyfile", "--adapter", "caddyfile")
	uid := strings.TrimSpace(b.in("docker", "run", "--rm", "--entrypoint", "id", "ghcr.io/teaarte/deck:candidate", "-u", "deck"))
	b.in("chown", "-R", uid, "/srv/bin", "/srv/home")
	b.in("chmod", "755", "/srv/bin/deck")
	// deck as a binary on a machine of its owner: its own folder, loopback, no image; the image only lends its programs.
	b.in("docker", "run", "-d", "--name", "deck-bin", "--network", "host", "--add-host", "github.com:"+releaseIP, "-u", "deck",
		"-e", "DECK_HOME=/srv/home", "-e", "DECK_IMAGE=", "-e", "DECK_PUBLIC=", "-e", "DECK_PROXY=", "-e", "DECK_ADDR=127.0.0.1:7700",
		"-v", "/srv/bin:/srv/bin", "-v", "/srv/home:/srv/home", "-v", "/srv/rel/cert.pem:/etc/ssl/certs/ca-certificates.crt:ro",
		"--entrypoint", "/srv/bin/deck", "ghcr.io/teaarte/deck:candidate")
	b.ctr, b.exe, b.home, b.origin = "deck-bin", "/srv/bin/deck", "/srv/home", "http://deck.localhost:7700"
	cookie := b.signIn()
	b.keep(cookie, v)
	b.logins()
	var ver struct {
		Version, Variant, Latest string
		Newer                    bool
	}
	_ = json.Unmarshal(b.must(cookie, "deck.version_get", map[string]any{}), &ver)
	if ver.Version != v || ver.Variant != variant {
		t.Fatalf("the binary of %s says it is %+v", v, ver)
	}
	_ = json.Unmarshal(b.must(cookie, "deck.update_check", map[string]any{}), &ver)
	if ver.Latest != candidateVersion || !ver.Newer {
		t.Fatalf("the binary of %s finds the release of this tree: %+v", v, ver)
	}
	b.must(cookie, "deck.update_install", map[string]any{})
	b.updated(cookie, v)
	_ = json.Unmarshal(b.must(cookie, "deck.version_get", map[string]any{}), &ver)
	if ver.Version != candidateVersion || ver.Variant != variant {
		t.Fatalf("the update is the binary of this tree of the variant of %s: %+v", v, ver)
	}
	if in := strings.Fields(b.in("sha256sum", "/srv/bin/deck")); len(in) == 0 || in[0] != built[variant] {
		t.Fatalf("the binary in place is %s of this tree: %v", variant, in)
	}
	harness.Until(t, "the binary before the update and the mark of the update to go once the new one serves", func() bool {
		return b.deck("deck", "sh", "-c", "test ! -e /srv/bin/deck.old && test ! -e "+home.Layout(b.home).Update()).Run() == nil
	})
	b.kept(cookie)
	b.devices(cookie)
	b.calm()
}

// put writes a file of the bare docker.
func (b *bare) put(path string, data []byte) {
	b.t.Helper()
	cmd := exec.Command("docker", "exec", "-i", b.name, "sh", "-c", "cat > "+path)
	cmd.Stdin = bytes.NewReader(data)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.t.Fatalf("put %s: %v\n%s", path, err, out)
	}
}

// token is the owner's token of v0.1, which its log carried.
func (b *bare) token() string {
	b.t.Helper()
	var m []string
	harness.Until(b.t, "the owner's token in the log of v0.1", func() bool {
		m = regexp.MustCompile(`\?token=([A-Za-z0-9_-]+)`).FindStringSubmatch(b.logs())
		return m != nil
	})
	return m[1]
}

// keep puts in a deck of version v what its owner keeps: a module with records, a secret it names and a document.
func (b *bare) keep(cookie, v string) {
	b.t.Helper()
	m, upsert, secret := keptManifest, "deck.record_upsert", map[string]any{"project": "kept", "name": "key", "value": keptSecret}
	if v == releases[0] {
		// v0.1 read the manifests of format 13, wrote records by records_upsert and kept a secret by workspace.
		m, upsert = harness.ManifestVersion(m, "13"), "deck.records_upsert"
		delete(secret, "project")
	}
	b.must(cookie, "deck.project_create", map[string]any{"name": "kept", "manifest": m})
	b.must(cookie, upsert, map[string]any{"project": "kept", "collection": "items",
		"records": "{\"name\": \"first\", \"size\": 1}\n{\"name\": \"second\", \"size\": 2}"})
	b.must(cookie, "deck.secret_set", secret)
	b.must(cookie, "deck.document_save", map[string]any{"project": "kept", "name": "notes/plan.md", "kind": "doc", "text": keptText})
}

// logins signs the CLIs in where the sign-in of v0.2 left them, not where this tree looks: a move would show.
func (b *bare) logins() {
	b.t.Helper()
	b.write(b.home+"/agents/claude/login/.credentials.json", claudeLogin, "deck")
	b.write(b.home+"/agents/codex/login/auth.json", codexLogin, "deck")
}

// updated waits for deck of this tree to answer in place of the one of version v.
func (b *bare) updated(cookie, v string) {
	b.t.Helper()
	var ver struct{ Version string }
	harness.UntilWithin(b.t, 4*time.Minute, "deck of this tree to answer in place of "+v, func() bool {
		out, ok := b.try(cookie, "deck.version_get", map[string]any{})
		return ok && json.Unmarshal(out, &ver) == nil && ver.Version != "" && ver.Version != v
	})
	b.t.Logf("updated from %s to %s", v, ver.Version)
}

// kept checks that deck keeps what keep and logins put in.
func (b *bare) kept(cookie string) {
	t := b.t
	t.Helper()
	var items []struct {
		Data struct {
			Name string
			Size int
		}
	}
	_ = json.Unmarshal(b.must(cookie, "deck.records_list", map[string]any{"project": "kept", "collection": "items"}), &items)
	var names []string
	for _, it := range items {
		names = append(names, fmt.Sprint(it.Data.Name, it.Data.Size))
	}
	if slices.Sort(names); !slices.Equal(names, []string{"first1", "second2"}) {
		t.Fatalf("the records of the module are kept: %v", names)
	}
	var secrets []struct {
		Name string
		Set  bool
	}
	_ = json.Unmarshal(b.must(cookie, "deck.secrets_list", map[string]any{"project": "kept"}), &secrets)
	if len(secrets) != 1 || secrets[0].Name != "key" || !secrets[0].Set {
		t.Fatalf("the secret the module names is set: %+v", secrets)
	}
	if v := b.sh("deck", "cat $(find "+home.Layout(b.home).State()+"/secrets -path '*/kept/key')"); strings.TrimSpace(v) != keptSecret {
		t.Fatalf("the secret keeps its value: %q", v)
	}
	if doc := b.must(cookie, "deck.document_get", map[string]any{"project": "kept", "name": "notes/plan.md"}); !strings.Contains(string(doc), keptText) {
		t.Fatalf("the document is kept: %s", doc)
	}
	l := home.Layout(b.home)
	if logins := b.sh("deck", "cat "+filepath.Join(l.Login("claude"), ".credentials.json")+" "+filepath.Join(l.Login("codex"), "auth.json")); !strings.Contains(logins, "sk-ant-ort01-old") ||
		!strings.Contains(logins, "sk-old") {
		t.Fatalf("the logins of the CLIs are kept:\n%s", logins)
	}
}

// devices checks that the device paired before the update stays signed in and deck pair pairs another.
func (b *bare) devices(cookie string) {
	t := b.t
	t.Helper()
	count := func(c string) int {
		var ds []struct{ Name string }
		_ = json.Unmarshal(b.must(c, "deck.devices_list", map[string]any{}), &ds)
		n := 0
		for _, d := range ds {
			if d.Name == "server test" {
				n++
			}
		}
		return n
	}
	before := count(cookie)
	if before == 0 {
		t.Fatal("the device paired before the update is listed")
	}
	if after := count(b.pair()); after != before+1 {
		t.Fatalf("deck pair pairs another device: %d, then %d", before, after)
	}
}

// calm checks that deck did not panic.
func (b *bare) calm() {
	b.t.Helper()
	if logs := b.logs(); strings.Contains(logs, "panic") {
		b.t.Fatalf("deck panicked:\n%s", logs)
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// selfSigned is a certificate of host that is its own authority, and its key, in PEM.
func selfSigned(t *testing.T, host string) (cert, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

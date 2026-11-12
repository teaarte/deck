package owner

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// keyManifest has a tool that sends $secret.svc and one that signs in with the secret auth names.
func keyManifest(url, auth string) string {
	return harness.Manifest(fmt.Sprintf(`{"tools": {"http": [
    {"name": "post", "description": "Posts", "method": "POST", "url": "%[1]s/post", "headers": {"X-Key": "$secret.svc"}, "input": {"type": "object"}},
    {"name": "signed", "description": "Signs", "method": "GET", "url": "%[1]s/signed", "auth": {"type": "secret", "secret": %[2]q}}]}}`, url, auth))
}

// TestTheSecretsOfBeforeMoveToTheirModulesOnce: an old secret goes to each module naming it, a key of a connection
// to keys, one nobody names to keys by its name or apart; all wait while a manifest does not read.
func TestTheSecretsOfBeforeMoveToTheirModulesOnce(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": svc.URL + "/v1"}, nil)
	e.Project("films", harness.Declared(keyManifest(svc.URL, "own")))
	e.Project("notes", harness.Declared(keyManifest(svc.URL, "openrouter")))
	e.Project("broken", harness.Declared(harness.PlainManifest))
	// A manifest a deck before left that no longer reads.
	e.PlantRevision("broken", "manifest.json", harness.Manifest(`{"commands": [{"name": 1}]}`))
	h := deckhome.Layout(home)
	old := h.Secrets(1)
	for name, value := range map[string]string{"svc": "shared-value", "own": "films-own", "openrouter": "sk-or-old", "left": "no-one's", "mail_key": "kept"} {
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, name), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A base before the move: the connection names the secret of its key.
	e.SQL(`UPDATE connection SET key_secret = 'openrouter' WHERE name = 'openrouter'`)
	// And the move is not done yet.
	e.SQL(`DELETE FROM mark WHERE name = 'secrets:moved'`)
	e = e.Restart()
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return "-"
		}
		return strings.TrimSpace(string(b))
	}
	if read(filepath.Join(old, "~before", "left")) != "no-one's" || read(filepath.Join(old, "~before", "svc")) != "shared-value" {
		t.Fatal("while a manifest does not read, no old secret goes")
	}
	e.Must("deck.manifest_save", map[string]any{"project": "broken", "manifest": harness.PlainManifest}, nil)
	e = e.Restart()

	for path, want := range map[string]string{
		filepath.Join(old, "films", "svc"): "shared-value", filepath.Join(old, "notes", "svc"): "shared-value",
		filepath.Join(old, "films", "own"): "films-own", filepath.Join(old, "notes", "openrouter"): "-",
		filepath.Join(h.Keys(1), "openrouter"): "sk-or-old", filepath.Join(h.Keys(1), "left"): "no-one's",
		filepath.Join(old, "~before", "left"): "-", filepath.Join(old, "~before", "mail_key"): "kept",
		filepath.Join(old, "svc"): "-", filepath.Join(old, "own"): "-", filepath.Join(old, "openrouter"): "-",
	} {
		if got := read(path); got != want {
			t.Fatalf("%s holds %q after the move, want %q", path, got, want)
		}
	}
	e.Must("films.post", map[string]any{}, nil)
	e.Must("notes.post", map[string]any{}, nil)
	e.Must("films.signed", map[string]any{}, nil)
	if code, out := e.Call("notes.signed", map[string]any{}); code == 200 || !strings.Contains(string(out), `secret "openrouter" of notes is not set`) {
		t.Fatalf("a module that named the key of a connection does not get it in the move: %d %s", code, out)
	}
	got := map[string]string{}
	for _, r := range svc.Requests() {
		got[r.Path] += r.Header.Get("X-Key") + r.Header.Get("Authorization") + ";"
	}
	if got["/post"] != "shared-value;shared-value;" || got["/signed"] != "Bearer films-own;" {
		t.Fatalf("both modules send their secrets after the move: %v", got)
	}

	e.Secret("films", "svc", "films-new")
	e = e.Restart()
	if read(filepath.Join(old, "films", "svc")) != "films-new" {
		t.Fatal("a second start moves nothing again")
	}
}

// TestADeletedModuleTakesItsSecretsAlong: deleting a module deletes its secrets and no other module's.
func TestADeletedModuleTakesItsSecretsAlong(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("films", harness.Declared(keyManifest("https://api.example", "own")))
	e.Project("notes", harness.Declared(keyManifest("https://api.example", "own")))
	e.Secret("films", "svc", "a")
	e.Secret("notes", "svc", "b")
	e.Must("deck.project_delete", map[string]any{"project": "films"}, nil)
	dir := deckhome.Layout(e.App.Cfg.Home).Secrets(1)
	if _, err := os.Stat(filepath.Join(dir, "films")); !os.IsNotExist(err) {
		t.Fatalf("the secrets of a deleted module are gone: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes", "svc")); string(b) != "b" {
		t.Fatalf("the secrets of another module stay: %q", b)
	}
}

// TestASnapshotHoldsNoSecretOrKey: a snapshot has the base and files but no secret of a module and no key of a
// connection, by path or by value.
func TestASnapshotHoldsNoSecretOrKey(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": "https://api.example/v1", "key": "sk-key-in-no-snapshot"}, nil)
	e.Project("films", harness.Declared(keyManifest("https://api.example", "own")))
	e.Secret("films", "svc", "secret-in-no-snapshot")
	var snap struct{ Path string }
	e.Must("deck.snapshot_create", nil, &snap)
	f, err := os.Open(snap.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		names = append(names, hd.Name)
		if strings.Contains(hd.Name, "secrets") || strings.Contains(hd.Name, "keys") ||
			strings.Contains(string(b), "secret-in-no-snapshot") || strings.Contains(string(b), "sk-key-in-no-snapshot") {
			t.Fatalf("a snapshot holds no secret or key: %s", hd.Name)
		}
	}
	if len(names) == 0 || !strings.HasSuffix(names[0], "deck.db") {
		t.Fatalf("the snapshot holds the base: %v", names)
	}
}

// TestAKeyOutlivesItsConnection: a connection removed and made again by its name has its key back, so a key of a
// provider is never lost with the connection; a model call through it sends it.
func TestAKeyOutlivesItsConnection(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": "https://api.example/v1", "key": "gsk-kept"}, nil)
	e.Must("deck.connection_delete", map[string]any{"name": "groq"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": "https://api.example/v1"}, nil)
	var out harness.ModelsOut
	e.Must("deck.models_list", nil, &out)
	i := slices.IndexFunc(out.Connections, func(c harness.ModelsOutConnection) bool { return c.Name == "groq" })
	if i < 0 || out.Connections[i].Key != "set" {
		t.Fatalf("the connection made again has its key: %+v", out.Connections)
	}
}

// TestASecretSetBeforeItsManifestNamesItIsSeen: a secret of a module its manifest does not name yet is listed with
// its mark and time, shown in the Secrets table of its Access tab and in the summary of the settings, and deleted there.
func TestASecretSetBeforeItsManifestNamesItIsSeen(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("mail", harness.Declared(harness.PostManifest("https://api.example", "")))
	e.Project("notes", harness.Declared(harness.PlainManifest))
	if rows := secretRows(e, "notes"); len(rows) != 0 {
		t.Fatalf("the Secrets table of a module without secrets is there and empty: %v", rows)
	}
	e.Secret("mail", "svc", "named-value")
	e.Secret("mail", "early", "early-value")
	var listed []struct {
		Project   string
		Name      string
		Set       bool
		Named     bool
		ChangedAt string `json:"changed_at"`
	}
	e.Must("deck.secrets_list", map[string]any{"project": "mail"}, &listed)
	if len(listed) != 2 || listed[1].Name != "early" || !listed[1].Set || listed[1].Named || listed[1].ChangedAt == "" ||
		listed[0].Name != "svc" || !listed[0].Named || strings.Contains(fmt.Sprint(listed), "value") {
		t.Fatalf("secrets_list gives the named secret and the one set before its manifest names it, no value: %+v", listed)
	}
	rows := secretRows(e, "mail")
	i := slices.IndexFunc(rows, func(r harness.ScreenRow) bool { return len(r.Cells) > 0 && fmt.Sprint(r.Cells[0]) == "early" })
	if i < 0 || !strings.Contains(fmt.Sprint(rows[i].Cells), "not named by the manifest") || strings.Contains(fmt.Sprint(rows), "early-value") {
		t.Fatalf("the Access tab shows the secret its manifest does not name: %+v", rows)
	}
	if !slices.ContainsFunc(rows[i].Actions, func(a map[string]any) bool { return a["command"] == "deck.secret_delete" }) {
		t.Fatalf("a secret set has a delete button: %+v", rows[i].Actions)
	}
	if summary := fmt.Sprint(e.Screen("", "settings", map[string]any{"tab": "Modules"})); !strings.Contains(summary, "early") || strings.Contains(summary, "early-value") {
		t.Fatalf("the settings sum up the secrets of the modules without a value: %s", summary)
	}
	e.Must("deck.secret_delete", map[string]any{"project": "mail", "name": "early"}, nil)
	e.Must("deck.secrets_list", map[string]any{"project": "mail"}, &listed)
	if len(listed) != 1 || listed[0].Name != "svc" {
		t.Fatalf("a deleted secret is gone from the list: %+v", listed)
	}
}

// secretRows are the rows of the Secrets table on the Access tab of a module's settings.
func secretRows(e *harness.Env, project string) []harness.ScreenRow {
	e.T.Helper()
	in := map[string]any{"tab": "Access"}
	data := e.Screen(project, "settings", in)
	for _, m := range e.ScreenMessages(project, "settings", in) {
		var uc struct{ Components []map[string]any }
		_ = json.Unmarshal(m["updateComponents"], &uc)
		for _, c := range uc.Components {
			if id, _ := c["id"].(string); c["component"] == "table" && c["title"] == "Secrets" && data[id] != nil {
				return harness.BlockData[[]harness.ScreenRow](e.T, data[id]["rows"])
			}
		}
	}
	e.T.Fatal("the Access tab has a Secrets table")
	return nil
}

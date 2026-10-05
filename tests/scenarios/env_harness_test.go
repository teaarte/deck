package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/web"
)

const token = "test-token"

type Env struct {
	T    *testing.T
	App  *app.App
	Fake *Fake
	URL  string
	// Side is the second origin: the frames of custom blocks.
	Side  string
	Clock *Clock
	// Browser is the folder of the fake browser server: its log of what it did.
	Browser string
	names   map[int64]string
	// Probes counts the reads of deck.probe, a read every test deck has.
	Probes *atomic.Int64
}

type probeIn struct {
	Days int64 `opt:"" desc:"Days"`
}

type probeRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// probe is deck.probe: two rows, and a count of its reads.
func probe(n *atomic.Int64) *registry.Command {
	return registry.Read("probe", "Probe", "Gives two rows and counts its reads.", registry.OwnerSide,
		func(context.Context, registry.Actor, probeIn) ([]probeRow, error) {
			n.Add(1)
			return []probeRow{{1, "a"}, {2, "b"}}, nil
		}).On("collection")
}

// Clock is the deck clock of a test: it stands still until the test moves it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// withClock runs deck on a test clock that starts at start.
func withClock(clock *Clock) func(*app.Config) {
	return func(c *app.Config) { c.Now = clock.Now }
}

func newEnv(t *testing.T, script Script, cfg ...func(*app.Config)) *Env {
	t.Helper()
	fake := &Fake{script: script}
	e := newEnvWith(t, t.TempDir(), fake, cfg...)
	e.Fake = fake
	return e
}

// newEnvWith starts deck on home with the given provider.
func newEnvWith(t *testing.T, home string, p provider.Provider, cfg ...func(*app.Config)) *Env {
	t.Helper()
	return newEnvBuiltin(t, home, []provider.Provider{p}, cfg...)
}

// newEnvBuiltin starts deck on home with the given built-in providers, the main one first.
func newEnvBuiltin(t *testing.T, home string, ps []provider.Provider, cfg ...func(*app.Config)) *Env {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	browser := t.TempDir()
	self, _ := os.Executable()
	c := app.Config{Home: home, MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second,
		Browser: []string{"env", "DECK_FAKE_MCP=browser", "DECK_FAKE_DIR=" + browser, self}}
	probes := &atomic.Int64{}
	c.Commands = []*registry.Command{probe(probes)}
	for _, f := range cfg {
		f(&c)
	}
	quiet := &Fake{name: "quiet", script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	a, err := app.New(c, ps[0], append(ps[1:], quiet)...)
	if err != nil {
		t.Fatal(err)
	}
	a.Asks.Poll = 20 * time.Millisecond
	srv := api.New(a, token, web.FS())
	side := httptest.NewServer(srv.Sandbox())
	srv.Pair(ts.Listener.Addr().(*net.TCPAddr).Port, side.Listener.Addr().(*net.TCPAddr).Port)
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() {
		_ = a.Close()
		ts.CloseClientConnections()
		ts.Close()
		side.Close()
	})
	e := &Env{T: t, App: a, URL: base, Side: side.URL, names: map[int64]string{}, Browser: browser, Probes: probes}
	// The master looks into failed processes on a provider of its own, so tests of failures see only their calls.
	e.must("deck.provider_set", map[string]any{"command": "master.on_error", "provider": "quiet"}, nil)
	// Its schedule stays out of the clocks of tests; a test of the master's own triggers resumes them.
	e.must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	return e
}

func (e *Env) do(method, path string, body any) (int, []byte) {
	e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// call invokes a registry command as the owner and returns the status and raw body.
func (e *Env) call(name string, input any) (int, []byte) {
	e.T.Helper()
	return e.do("POST", "/api/commands/"+name, input)
}

// must invokes a command, fails the test on an error status and decodes the result into out.
func (e *Env) must(name string, input any, out any) {
	e.T.Helper()
	code, body := e.call(name, input)
	if code != 200 {
		e.T.Fatalf("%s: %d %s", name, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			e.T.Fatalf("%s: decode %s: %v", name, body, err)
		}
	}
}

func (e *Env) createProject(name, manifest string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.must("deck.project_create", map[string]any{"name": name, "manifest": manifest}, &p)
	e.names[p.ID] = name
	return p.ID
}

func (e *Env) run(project int64, command string) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.must(e.names[project]+"."+command, map[string]any{}, &r)
	return r.Process
}

func (e *Env) projectID(name string) int64 {
	for id, n := range e.names {
		if n == name {
			return id
		}
	}
	e.T.Fatalf("no project %s", name)
	return 0
}

// olderBase takes the base of deck back to the schema of a migration version, as a deck of then left it.
func (e *Env) olderBase(version int) {
	e.T.Helper()
	undo := map[int][]string{
		4: {`PRAGMA foreign_keys = OFF`, `CREATE TABLE ask_old (id INTEGER PRIMARY KEY AUTOINCREMENT,
		   workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		   project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
		   process_id INTEGER REFERENCES process(id) ON DELETE CASCADE, kind TEXT NOT NULL, text TEXT NOT NULL,
		   payload TEXT NOT NULL DEFAULT '{}', answer TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'open',
		   created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), answered_at TEXT, expires_at TEXT, delivered_at TEXT)`,
			`INSERT INTO ask_old SELECT * FROM ask`, `DROP TABLE ask`, `ALTER TABLE ask_old RENAME TO ask`, `PRAGMA foreign_keys = ON`},
		3: {`CREATE TABLE llm_old AS SELECT id, process_id, gen_ai_provider_name, gen_ai_request_model, gen_ai_response_model,
		   gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens,
		   gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd, created_at FROM llm_call`,
			`DROP TABLE llm_call`, `ALTER TABLE llm_old RENAME TO llm_call`},
		8: {`ALTER TABLE process DROP COLUMN alone`},
		6: {`ALTER TABLE feed DROP COLUMN read_at`},
		5: {`ALTER TABLE project DROP COLUMN imported_from`, `ALTER TABLE feed DROP COLUMN for_master`},
		2: {`ALTER TABLE project DROP COLUMN granted_revision`},
	}
	var top int
	if err := e.App.Store.DB.QueryRow(`SELECT max(version_id) FROM goose_db_version`).Scan(&top); err != nil {
		e.T.Fatal(err)
	}
	for v := top; v > version; v-- {
		for _, q := range append(undo[v], fmt.Sprintf(`DELETE FROM goose_db_version WHERE version_id = %d`, v)) {
			if _, err := e.App.Store.DB.Exec(q); err != nil {
				e.T.Fatalf("undo migration %d: %v", v, err)
			}
		}
	}
}

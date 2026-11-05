package owner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"deck2/internal/node"
	"deck2/tests/harness"
)

// nodeModels is a server of models on the machine of a node: text, and speech as big as a song.
func nodeModels(t *testing.T, text string) *harness.FakeModels {
	return harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(text)
		},
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) {
			return 200, "audio/mpeg", "ID3" + strings.Repeat("a", 24<<20)
		},
	})
}

// asked are the calls of models a server got, without the reads of its list of models.
func asked(f *harness.FakeModels) []harness.ModelCall {
	var out []harness.ModelCall
	for _, c := range f.Calls() {
		if c.Path != "/v1/models" {
			out = append(out, c)
		}
	}
	return out
}

type connectionRow struct {
	Name, Node string
	Online     bool
}

func connectionOf(e *harness.Env, name string) connectionRow {
	e.T.Helper()
	var out struct{ Connections []connectionRow }
	e.Must("deck.models_list", map[string]any{}, &out)
	for _, c := range out.Connections {
		if c.Name == name {
			return c
		}
	}
	e.T.Fatalf("no connection %s: %+v", name, out)
	return connectionRow{}
}

// TestAModelOfANodeIsCalledThroughItsNode: a connection the owner puts on a node goes over the node's connection to
// the address of its machine, a big answer of speech whole; the health of the node says what it serves.
func TestAModelOfANodeIsCalledThroughItsNode(t *testing.T) {
	t.Parallel()
	api := nodeModels(t, "from the node")
	e := harness.New(t)
	n := e.Node("Mac", api.URL)
	if h := e.WaitHealth("node:Mac", "ok"); !strings.Contains(h.Error, "no connection goes through it yet") {
		t.Fatalf("a node with no connection says how to give it one: %+v", h)
	}
	e.Refused("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": "https://openrouter.ai/api/v1", "node": "Mac"},
		422, "a node serves only the addresses of its own machine")
	e.Refused("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": api.URL + "/v1", "node": "Linux"}, 404, `no node "Linux"`)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": api.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "qwen", "name": "qwen", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "qwen3-tts", "name": "voice", "kind": "tts", "options": `{"voice": "own"}`}, nil)
	if c := connectionOf(e, "media"); c.Node != "Mac" || !c.Online {
		t.Fatalf("the models list says the connection goes through its node: %+v", c)
	}
	var said struct{ Text string }
	e.Must("deck.model_call", map[string]any{"kind": "text", "model": "qwen", "prompt": "hi"}, &said)
	if said.Text != "from the node" {
		t.Fatalf("the model of the node answers: %+v", said)
	}
	var speech struct {
		Type string
		Size int64
	}
	e.Must("deck.model_call", map[string]any{"kind": "tts", "model": "voice", "text": "Hello."}, &speech)
	if speech.Type != "audio/mpeg" || speech.Size != 3+24<<20 {
		t.Fatalf("a big answer of the node comes whole: %+v", speech)
	}
	if calls := asked(api); len(calls) != 2 || calls[0].Path != "/v1/chat/completions" || calls[1].Path != "/v1/audio/speech" {
		t.Fatalf("the server of the node gets the calls: %+v", calls)
	}
	if h := e.WaitHealth("node:Mac", "ok"); !strings.Contains(h.Error, "it serves media") {
		t.Fatalf("health says what the node serves: %+v", h)
	}
	n.Stop()
}

// TestANodeOffHandsItsCallsToTheNextModel: a call of the model of a node that is off goes to the next model at once,
// never to the node's machine by deck itself; health says which node and what to run; back, the node takes calls.
func TestANodeOffHandsItsCallsToTheNextModel(t *testing.T) {
	t.Parallel()
	local, cloud := nodeModels(t, "from the node"), nodeModels(t, "from the cloud")
	e := harness.New(t)
	n := e.Node("Mac", local.URL)
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": local.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "cloud", "api": "openai", "url": cloud.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "local", "model": "qwen", "name": "small", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cloud", "model": "qwen", "name": "big", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"small", "big"}}, nil)
	ask := func() string {
		var said struct{ Text string }
		e.Must("deck.model_call", map[string]any{"kind": "text", "prompt": "hi"}, &said)
		return said.Text
	}
	if got := ask(); got != "from the node" {
		t.Fatalf("the first model, on the node, answers: %q", got)
	}
	n.Stop()
	h := e.WaitHealth("node:Mac", "warning")
	if !strings.Contains(h.Error, "node Mac is offline since") || !strings.Contains(h.Error, "the calls of local go to the next model") ||
		!strings.Contains(h.Error, "deck -join "+e.URL+" --install") {
		t.Fatalf("health says which node is off and what to run on its machine: %+v", h)
	}
	if c := connectionOf(e, "local"); c.Online {
		t.Fatalf("the connection of a node that is off is not online: %+v", c)
	}
	if got := ask(); got != "from the cloud" {
		t.Fatalf("the next model takes the call of a node that is off: %q", got)
	}
	if len(asked(local)) != 1 {
		t.Fatalf("deck reaches the machine of the node only through it: %+v", asked(local))
	}
	n.Start()
	e.WaitHealth("node:Mac", "ok")
	if got := ask(); got != "from the node" {
		t.Fatalf("the node back, its model answers again: %q", got)
	}
}

// TestARevokedNodeLosesItsConnectionAndItsKeyOpensNothing: the key of a node opens only its connection, no command;
// a revoke ends the connection at once, the node's next try is refused, and its health row goes.
func TestARevokedNodeLosesItsConnectionAndItsKeyOpensNothing(t *testing.T) {
	t.Parallel()
	api := nodeModels(t, "from the node")
	e := harness.New(t)
	n := e.Node("Mac", api.URL)
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": api.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "local", "model": "qwen", "name": "small", "kind": "text"}, nil)
	e.WaitHealth("node:Mac", "ok")
	if code, out := e.As(n.Joined.Key).Call("deck.devices_list", map[string]any{}); code != http.StatusUnauthorized {
		t.Fatalf("the key of a node calls no command: %d %s", code, out)
	}
	e.Must("deck.device_revoke", map[string]any{"device": n.Joined.Device}, nil)
	e.Until("the connection of the revoked node to end", func() bool { return !connectionOf(e, "local").Online })
	e.Until("the health row of the revoked node to go", func() bool { return e.HealthRowOf("node:Mac") == nil })
	if code, out := e.Call("deck.model_call", map[string]any{"kind": "text", "model": "small", "prompt": "hi"}); code == http.StatusOK ||
		!strings.Contains(string(out), "the node Mac is not connected to deck") || len(asked(api)) != 0 {
		t.Fatalf("a call of the revoked node's model reaches nothing: %d %s", code, out)
	}
	ok, err := node.Connect(context.Background(), n.Joined, n.Serving)
	if ok || !errors.Is(err, node.ErrRefused) {
		t.Fatalf("deck refuses the key of a revoked node: %v %v", ok, err)
	}
}

// freePair is a port of loopback free with the next one, on IPv4 and IPv6, where a deck of a test starts again.
func freePair(t *testing.T) int {
	t.Helper()
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		free := true
		for _, at := range []string{"127.0.0.1:" + strconv.Itoa(port+1), "[::1]:" + strconv.Itoa(port), "[::1]:" + strconv.Itoa(port+1)} {
			if l, err := net.Listen("tcp", at); err == nil {
				_ = l.Close()
			} else {
				free = false
			}
		}
		_ = ln.Close()
		if free {
			return port
		}
	}
	t.Fatal("no two free ports in a row")
	return 0
}

// TestANodeJoinsByItsCommandAndComesBackAfterDeckRestarts: deck and node are two processes; deck -join pairs by the
// command of deck pair --node, keeps its key for its user alone, serves its model and comes back after deck restarts.
func TestANodeJoinsByItsCommandAndComesBackAfterDeckRestarts(t *testing.T) {
	// Not beside the other tests: its deck starts again on its port, which another test could take meanwhile.
	api := nodeModels(t, "from the node")
	bin := buildDeck(t)
	home, nodeHome := t.TempDir(), t.TempDir()
	address := "127.0.0.1:" + strconv.Itoa(freePair(t))
	d := runDeck(t, bin, home, []string{"-addr", address})
	out, err := d.pair("--node", "--name", "Mac")
	join := strings.Fields(lineOf(out, "deck -join http://"+address+" "))
	if err != nil || len(join) != 5 || join[4] != "--install" {
		t.Fatalf("deck pair --node gives the command to run on the node: %v %s", err, out)
	}
	port := api.URL[strings.LastIndex(api.URL, ":")+1:]
	n := exec.Command(bin, "-join", join[2], join[3], "--allow", port+",claude-code")
	n.Env = append(os.Environ(), "DECK_HOME="+nodeHome)
	stderr, err := n.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var said []string
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Process.Kill(); _ = n.Wait() })
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			mu.Lock()
			said = append(said, sc.Text())
			mu.Unlock()
		}
	}()
	defer func() {
		if t.Failed() {
			mu.Lock()
			t.Logf("the node said:\n%s", strings.Join(said, "\n"))
			mu.Unlock()
		}
	}()
	online := func(d *deckBinary) bool {
		_, out, err := d.call("deck.models_list", map[string]any{})
		var v struct{ Connections []connectionRow }
		_ = json.Unmarshal([]byte(out), &v)
		return err == nil && slices.ContainsFunc(v.Connections, func(c connectionRow) bool { return c.Name == "local" && c.Online })
	}
	ask := func(d *deckBinary) string {
		code, out, err := d.call("deck.model_call", map[string]any{"kind": "text", "model": "small", "prompt": "hi"})
		var v struct{ Text string }
		_ = json.Unmarshal([]byte(out), &v)
		if err != nil || code != 200 {
			t.Fatalf("a call of the node's model: %d %s %v", code, out, err)
		}
		return v.Text
	}
	harness.Until(t, "the node to pair", func() bool {
		_, err := os.Stat(filepath.Join(nodeHome, "state", "node", "joined.json"))
		return err == nil
	})
	if fi, err := os.Stat(filepath.Join(nodeHome, "state", "node", "joined.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the key of the node is its user's alone: %v %v", fi, err)
	}
	if b, err := os.ReadFile(filepath.Join(nodeHome, "state", "node", "allowed")); err != nil || !strings.Contains(string(b), port+",claude-code") {
		t.Fatalf("the node keeps what its owner allows in its own file: %s %v", b, err)
	}
	for _, in := range []map[string]any{
		{"name": "local", "api": "openai", "url": api.URL + "/v1", "local": true, "node": "Mac"},
	} {
		if code, out, err := d.call("deck.connection_set", in); err != nil || code != 200 {
			t.Fatalf("connection: %d %s %v", code, out, err)
		}
	}
	if code, out, err := d.call("deck.model_set", map[string]any{"connection": "local", "model": "qwen", "name": "small", "kind": "text"}); err != nil || code != 200 {
		t.Fatalf("model: %d %s %v", code, out, err)
	}
	harness.Until(t, "the node to connect", func() bool { return online(d) })
	if got := ask(d); got != "from the node" {
		t.Fatalf("the node's model answers through the node: %q", got)
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	_ = d.cmd.Wait()
	d2 := runDeck(t, bin, home, []string{"-addr", address})
	harness.Until(t, "the node to connect to deck started again", func() bool { return online(d2) })
	if got := ask(d2); got != "from the node" {
		t.Fatalf("the node's model answers after deck started again: %q", got)
	}
}

// TestANodeServesOnlyThePortsItsOwnerAllows: a call to a port the node's owner did not allow is refused on the node,
// goes to the next model, and health says what to add; an edit of the node's list holds at once, deck never widens it.
func TestANodeServesOnlyThePortsItsOwnerAllows(t *testing.T) {
	t.Parallel()
	local, cloud := nodeModels(t, "from the node"), nodeModels(t, "from the cloud")
	e := harness.New(t)
	n := e.Node("Mac")
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": local.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "cloud", "api": "openai", "url": cloud.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "local", "model": "qwen", "name": "small", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cloud", "model": "qwen", "name": "big", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"small", "big"}}, nil)
	ask := func() string {
		var said struct{ Text string }
		e.Must("deck.model_call", map[string]any{"kind": "text", "prompt": "hi"}, &said)
		return said.Text
	}
	if got := ask(); got != "from the cloud" {
		t.Fatalf("a call to a port the node does not allow goes to the next model: %q", got)
	}
	if len(asked(local)) != 0 {
		t.Fatalf("the node passes nothing to a port its owner did not allow: %+v", asked(local))
	}
	port := local.URL[strings.LastIndex(local.URL, ":")+1:]
	h := e.WaitHealth("node:Mac", "warning")
	if !strings.Contains(h.Error, "port "+port+" is not allowed on it") || !strings.Contains(h.Error, "--install --allow "+port) {
		t.Fatalf("health says which port to allow on the node: %+v", h)
	}
	if code, out := e.Call("deck.model_call", map[string]any{"kind": "text", "model": "small", "prompt": "hi"}); code == http.StatusOK ||
		!strings.Contains(string(out), "port "+port+" is not allowed on the node Mac") {
		t.Fatalf("a call of the model alone says the node refused its port: %d %s", code, out)
	}
	n.Allow(local.URL)
	if got := ask(); got != "from the node" {
		t.Fatalf("the port allowed in the node's list, its model answers: %q", got)
	}
}

// freezer passes a connection to deck until it freezes: then nothing goes either way and nothing closes, as when the
// machine of a node sleeps.
type freezer struct {
	ln     net.Listener
	frozen chan struct{}
	once   sync.Once
}

func newFreezer(t *testing.T, to string) *freezer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &freezer{ln: ln, frozen: make(chan struct{})}
	gone := make(chan struct{})
	t.Cleanup(func() { close(gone); _ = ln.Close() })
	// The node asks for deck at the freezer's address; deck answers only at its own.
	host := []byte("Host: " + strings.TrimPrefix(f.URL(), "http://"))
	pass := func(dst, src net.Conn, first bool) {
		buf := make([]byte, 32<<10)
		for {
			k, err := src.Read(buf)
			if first && k > 0 {
				first = false
				b := bytes.Replace(buf[:k], host, []byte("Host: "+to), 1)
				k = copy(buf, b)
			}
			select {
			case <-f.frozen:
				<-gone
				return
			default:
			}
			if k > 0 {
				if _, werr := dst.Write(buf[:k]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = dst.Close()
				return
			}
		}
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d, err := net.Dial("tcp", to)
			if err != nil {
				_ = c.Close()
				continue
			}
			go pass(d, c, true)
			go pass(c, d, false)
		}
	}()
	return f
}

func (f *freezer) URL() string { return "http://" + f.ln.Addr().String() }
func (f *freezer) freeze()     { f.once.Do(func() { close(f.frozen) }) }

// TestANodeGoneSilentBeforeItsAnswerHandsTheCallOn: the machine of a node falls asleep with a call on it and its
// connection stays open; deck's pings find it gone and the call goes to the next model rather than failing.
func TestANodeGoneSilentBeforeItsAnswerHandsTheCallOn(t *testing.T) {
	t.Parallel()
	cloud := nodeModels(t, "from the cloud")
	var f *freezer
	local := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			f.freeze()
			time.Sleep(time.Minute)
			return 200, "application/json", harness.ChatText("from the node")
		},
	})
	e := harness.New(t)
	f = newFreezer(t, strings.TrimPrefix(e.URL, "http://"))
	n := e.PairNode("Mac")
	n.Joined.Deck = f.URL()
	n.Allow(local.URL)
	n.Start()
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": local.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "cloud", "api": "openai", "url": cloud.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "local", "model": "qwen", "name": "small", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cloud", "model": "qwen", "name": "big", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"small", "big"}}, nil)
	e.Until("the node to connect", func() bool { return connectionOf(e, "local").Online })
	var said struct{ Text string }
	e.Must("deck.model_call", map[string]any{"kind": "text", "prompt": "hi"}, &said)
	if said.Text != "from the cloud" {
		t.Fatalf("the call on a node gone silent goes to the next model: %+v", said)
	}
	if c := connectionOf(e, "local"); c.Online {
		t.Fatalf("a node gone silent is offline: %+v", c)
	}
}

// TestANodeKeepsAFolderApartFromDecks: a node's folder is .deck2-node, apart from the folder and lock of a deck on the
// same machine; the key of a node joined before, in deck's .deck2, moves there.
func TestANodeKeepsAFolderApartFromDecks(t *testing.T) {
	t.Parallel()
	bin := buildDeck(t)
	user := t.TempDir()
	old := filepath.Join(user, ".deck2", "state", "node", "joined.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte(`{"deck":"http://127.0.0.1:9","key":"k","device":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n := exec.Command(bin, "-join", "http://127.0.0.1:9")
	n.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "DECK_HOME=") || strings.HasPrefix(kv, "HOME=") }),
		"HOME="+user)
	out, err := n.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Process.Kill(); _ = n.Wait() })
	lines := make(chan string, 10)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	moved := harness.Receive(t, "the node to say its key moved", lines)
	if !strings.Contains(moved, "moved from "+filepath.Join(user, ".deck2", "state", "node")+" to "+filepath.Join(user, ".deck2-node", "state", "node")) {
		t.Fatalf("the node says where its key went: %s", moved)
	}
	if nothing := harness.Receive(t, "the node to say what deck may use", lines); !strings.Contains(nothing, "Deck may use nothing on this machine yet") ||
		!strings.Contains(nothing, "--allow") {
		t.Fatalf("a node that allows nothing says how to allow: %s", nothing)
	}
	if _, err := os.Stat(filepath.Join(user, ".deck2-node", "state", "node", "joined.json")); err != nil {
		t.Fatalf("the key is in the node's own folder: %v", err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no key is left in deck's folder: %v", err)
	}
}

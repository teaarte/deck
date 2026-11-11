package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/fault"
	"deck2/internal/home"
	"deck2/internal/node"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
	"deck2/internal/sandbox"
	"deck2/internal/script"
	"deck2/internal/untrusted"
)

// Server runs the turns of deck's agents on a node, in the node's own folder, boxes and sign-ins.
type Server struct {
	Home    home.Layout
	Sandbox *sandbox.Sandbox
	// Allowed is what the owner of the node allows, read at each call.
	Allowed func() node.Allowed
	// Back is deck's side of the turns on the node's loopback.
	Back string
	// Build makes the provider of a spec; the adapters of deck with the programs found on the node when nil.
	Build func(spec Spec, api func(provider.ModelHooks) http.Handler) (provider.Provider, error)
	// Folder is the real path of a folder of the node's machine its owner allows deck; nil allows none.
	Folder func(path string) (string, error)

	mu    sync.Mutex
	turns map[string]*running
}

// running is a turn of a session on the node: the files it was handed, and done once it ended.
type running struct {
	before map[string]stamp
	roots  []string
	done   chan struct{}
}

// programs are the programs of the CLI agents deck runs, by the agent.
var programs = map[string]string{"claude-code": "claude", "codex": "codex"}

// Sweep removes the folders of turns a node before this one left.
func (s *Server) Sweep() { _ = os.RemoveAll(filepath.Dir(s.Home.Mirror("x"))) }

// Has are the agents the node runs for deck: allowed by its owner and found on its machine.
func (s *Server) Has() []string {
	var out []string
	for _, a := range s.Allowed().Agents {
		bin := programs[a]
		if bin == "" && acp.Agents[a].Start != nil {
			bin = a
		}
		if bin == "" {
			continue
		}
		if _, err := sandbox.Find(bin); err == nil || s.Build != nil {
			out = append(out, a)
		}
	}
	return out
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == turnPath && r.Method == http.MethodPost:
		s.turn(w, r)
	case r.URL.Path == filesPath && r.Method == http.MethodPut:
		s.take(w, r)
	case r.URL.Path == gonePath && r.Method == http.MethodGet:
		s.gone(w, r)
	case r.URL.Path == filesPath && r.Method == http.MethodGet:
		s.give(w, r)
	case r.URL.Path == filesPath && r.Method == http.MethodDelete:
		s.drop(w, r)
	case r.URL.Path == checkPath && r.Method == http.MethodPost:
		s.check(w, r)
	case r.URL.Path == loginPath && r.Method == http.MethodPost:
		s.login(w, r)
	case r.URL.Path == scriptPath && r.Method == http.MethodPost:
		s.script(w, r)
	default:
		http.Error(w, "deck node: not found", http.StatusNotFound)
	}
}

// build is the provider of a spec the owner of the node allows; deck's side of a turn serves the model of an agent.
func (s *Server) build(spec Spec, side string) (provider.Provider, error) {
	if !s.Allowed().Agent(spec.Agent) {
		return nil, fmt.Errorf("agent %s is not allowed on this node: on its machine add it with deck -join <deck's address> --install --allow %s", spec.Agent, spec.Agent)
	}
	api := func(h provider.ModelHooks) http.Handler { return s.model(side, h) }
	if s.Build != nil {
		return s.Build(spec, api)
	}
	switch {
	case spec.ACP:
		return acp.New(acp.Config{Name: spec.Name, Agent: spec.Agent, Model: spec.Model, NoCloud: spec.NoCloud, CallLimit: spec.CallLimit,
			CheckCeiling: spec.CheckCeiling, API: api, Home: s.Home, Sandbox: s.Sandbox})
	case spec.Agent == "claude-code":
		return with(claudecode.New(string(s.Home), s.Sandbox), spec), nil
	case spec.Agent == "codex":
		return with(codex.New(string(s.Home), s.Sandbox), spec), nil
	}
	return nil, fmt.Errorf("this node runs no agent %q", spec.Agent)
}

// with is a CLI on the model of a spec, or on its own default.
func with(cli provider.Modeled, spec Spec) provider.Provider {
	if spec.Model == "" {
		return cli.(provider.Provider)
	}
	return cli.With(spec.Name, spec.Model, spec.Effort)
}

// toDeck is deck's side of a turn or script on the node, under prefix.
func (s *Server) toDeck(side, prefix string) *httputil.ReverseProxy {
	to, _ := url.Parse(s.Back)
	return &httputil.ReverseProxy{FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &url.URL{Scheme: to.Scheme, Host: to.Host, Path: "/" + side + prefix + pr.In.URL.Path, RawQuery: pr.In.URL.RawQuery}
			pr.Out.Host = to.Host
		}}
}

// folders are the real paths of folders of the node's machine its owner allows deck; a folder not allowed is a
// refusal deck shows in the node's health.
func (s *Server) folders(w http.ResponseWriter, paths []string) ([]string, bool) {
	var out []string
	for _, p := range paths {
		if s.Folder == nil {
			http.Error(w, "deck node: this node gives deck no folders", http.StatusForbidden)
			return nil, false
		}
		real, err := s.Folder(p)
		var no *node.NotAllowed
		if errors.As(err, &no) {
			w.Header().Set(folderHeader, p)
		}
		if err != nil {
			http.Error(w, "deck node: "+err.Error(), http.StatusForbidden)
			return nil, false
		}
		out = append(out, real)
	}
	return out, true
}

// model is deck's proxy of the model of an agent on the node, reached at deck's side of its turn; it hears each answer
// as deck's own proxy would.
func (s *Server) model(side string, h provider.ModelHooks) http.Handler {
	p := s.toDeck(side, "/model")
	p.ModifyResponse = func(resp *http.Response) error {
		if h.Answer == nil {
			return nil
		}
		a := provider.Answer{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Past: resp.Header.Get(provider.PastLimit) != ""}
		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
			resp.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(strings.NewReader(string(b)), resp.Body), resp.Body}
			a.Words = string(b)
		}
		h.Answer(a)
		return nil
	}
	return p
}

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// session is the session of a call, a name of a folder.
func session(r *http.Request) (string, error) {
	s := r.URL.Query().Get("session")
	if !sessionName.MatchString(s) {
		return "", errors.New("deck node: a session is letters, digits, - and _")
	}
	return s, nil
}

// root checks the folders of a turn by their paths under deck's folder: they are in its work folder.
func root(names ...string) error {
	for _, n := range names {
		if n != "" && (!filepath.IsLocal(filepath.FromSlash(n)) || !strings.HasPrefix(n, "work/")) {
			return fmt.Errorf("deck node: %q is no folder of a turn", n)
		}
	}
	return nil
}

// take keeps the files deck hands a turn of a session, in place of those of the session before.
func (s *Server) take(w http.ResponseWriter, r *http.Request) {
	sess, err := session(r)
	roots := r.URL.Query()["roots"]
	if err == nil {
		err = root(roots...)
	}
	if err == nil && len(roots) == 0 {
		err = errors.New("deck node: a turn has folders")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A turn of the session cut off with its connection may still be ending; its folders are not taken from under it.
	if _, _, err := s.ended(r.Context(), sess); err != nil && !errors.Is(err, errNoTurn) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	dir := s.Home.Mirror(sess)
	if err := os.RemoveAll(dir); err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusInternalServerError)
		return
	}
	err = home.Unpack(dir, r.Body, func(name string) bool { return under(name, roots) }, nil)
	for _, rt := range roots {
		if err == nil {
			err = os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rt)), 0o700)
		}
	}
	var before map[string]stamp
	if err == nil {
		before, err = walk(dir, roots)
	}
	if err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.turns == nil {
		s.turns = map[string]*running{}
	}
	t := s.turns[sess]
	if t == nil {
		t = &running{done: make(chan struct{})}
		close(t.done)
		s.turns[sess] = t
	}
	t.before, t.roots = before, roots
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

var errNoTurn = errors.New("deck node: no turn of this session left files here")

// ended waits for the turn of a session to end and gives the files it was handed and their folders.
func (s *Server) ended(ctx context.Context, sess string) (map[string]stamp, []string, error) {
	s.mu.Lock()
	t := s.turns[sess]
	var done chan struct{}
	if t != nil {
		done = t.done
	}
	s.mu.Unlock()
	if t == nil {
		return nil, nil, errNoTurn
	}
	select {
	case <-done:
	case <-time.After(time.Minute):
		return nil, nil, errors.New("deck node: the turn of this session has not ended")
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return t.before, t.roots, nil
}

// changes are the files a turn of a session changed and those it removed.
func (s *Server) changes(ctx context.Context, sess string) (names, gone []string, n int64, err error) {
	before, roots, err := s.ended(ctx, sess)
	if err != nil {
		return nil, nil, 0, err
	}
	now, err := walk(s.Home.Mirror(sess), roots)
	if err != nil {
		return nil, nil, 0, err
	}
	names, gone = changed(before, now)
	for _, name := range names {
		n += now[name].Size
	}
	return names, gone, n, nil
}

// gone says what the files a turn changed hold and which files it removed.
func (s *Server) gone(w http.ResponseWriter, r *http.Request) {
	sess, err := session(r)
	var gone []string
	var n int64
	if err == nil {
		_, gone, n, err = s.changes(r.Context(), sess)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"size": n, "gone": append([]string{}, gone...)})
}

// give sends the files a turn of a session changed.
func (s *Server) give(w http.ResponseWriter, r *http.Request) {
	sess, err := session(r)
	var names []string
	if err == nil {
		names, _, _, err = s.changes(r.Context(), sess)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	_ = pack(w, s.Home.Mirror(sess), names)
}

// drop removes the files of a session deck took back.
func (s *Server) drop(w http.ResponseWriter, r *http.Request) {
	sess, err := session(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if t := s.turns[sess]; t != nil {
		select {
		case <-t.done:
			delete(s.turns, sess)
			_ = os.RemoveAll(s.Home.Mirror(sess))
		default:
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// turn runs a turn deck hands the node, in the folders it handed it, and tells deck what it does as it does it.
func (s *Server) turn(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	var st start
	if err := dec.Decode(&st); err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusBadRequest)
		return
	}
	q := st.Request
	if !sessionName.MatchString(q.SessionID) || !strings.HasPrefix(q.MCP, "/") || !sessionName.MatchString(st.Side) {
		http.Error(w, "deck node: a turn names its session and deck's side", http.StatusBadRequest)
		return
	}
	if err := root(slices.Concat([]string{q.Cwd, q.Data}, q.Dirs, q.Given)...); err != nil || (q.Cwd == "") == (q.Folder == "") {
		http.Error(w, "deck node: a turn works in a folder of deck's work or in one of this machine", http.StatusBadRequest)
		return
	}
	paths := q.Read
	if q.Folder != "" {
		paths = append([]string{q.Folder}, q.Read...)
	}
	folder, ok := s.folders(w, paths)
	if !ok {
		return
	}
	pv, err := s.build(st.Spec, st.Side)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	done, ok := s.begin(w, q.SessionID)
	if !ok {
		return
	}
	defer close(done)
	dir := s.Home.Mirror(q.SessionID)
	at := func(rel string) string {
		if rel == "" {
			return ""
		}
		return filepath.Join(dir, filepath.FromSlash(rel))
	}
	here := strings.NewReplacer(strings.TrimRight(st.Root, "/")+"/", dir+"/")
	skills := map[string]string{}
	for k, v := range q.Skills {
		skills[k] = here.Replace(v)
	}
	req := provider.Request{Project: q.Project, Cwd: at(q.Cwd), Repo: q.Repo, Prompt: here.Replace(q.Prompt), ResultSchema: q.ResultSchema,
		Skills: skills, MCPURL: s.Back + "/" + st.Side + q.MCP, Data: at(q.Data), Hosts: q.Hosts, GPU: q.GPU, AllowedTools: q.AllowedTools,
		PermissionMCP: q.PermissionMCP, SessionID: q.SessionID, Resume: q.Resume}
	if q.Folder != "" {
		req.Cwd, req.Read, req.Folder = folder[0], folder[1:], true
		_, err := os.Stat(filepath.Join(req.Cwd, ".git"))
		req.Repo = err == nil
	} else {
		req.Read = folder
	}
	for _, d := range q.Dirs {
		req.Dirs = append(req.Dirs, at(d))
	}
	for _, d := range q.Given {
		req.Given = append(req.Given, at(d))
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	rc := http.NewResponseController(w)
	send := func(f frame) {
		b, _ := json.Marshal(f)
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
		_ = rc.Flush()
	}
	_ = rc.Flush()
	req.OnEvent = func(e provider.Event) { send(frame{Event: &e}) }
	req.OnUsage = func(u provider.Usage) { send(frame{Usage: &u}) }
	ctx := r.Context()
	if q.Live {
		live := make(chan provider.Message)
		req.Live = live
		go func() {
			for {
				var f frame
				if dec.Decode(&f) != nil {
					return
				}
				if f.Live == nil {
					continue
				}
				taken := make(chan bool, 1)
				select {
				case live <- provider.Message{Text: *f.Live, Taken: taken}:
				case <-ctx.Done():
					return
				}
				var v bool
				select {
				case v = <-taken:
				case <-ctx.Done():
					return
				}
				send(frame{Taken: &v})
			}
		}()
	}
	res, err := pv.Run(ctx, req)
	if ctx.Err() != nil {
		return
	}
	e := errorOf(err)
	e.Result = res
	if e.Error != "" {
		e.Error = untrusted.Cut(e.Error, 8000)
	}
	send(frame{Done: &e})
}

// begin marks the session of a call running once deck handed it its files and no run of it still goes; done closes as
// the run ends.
func (s *Server) begin(w http.ResponseWriter, sess string) (chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turns[sess]
	if t == nil {
		http.Error(w, "deck node: deck handed no files of this run", http.StatusConflict)
		return nil, false
	}
	select {
	case <-t.done:
	default:
		http.Error(w, "deck node: a run of this session still goes", http.StatusConflict)
		return nil, false
	}
	done := make(chan struct{})
	t.done = done
	return done, true
}

// script runs a script deck hands the node in its box, in the folders deck handed it and those of its project on the
// node's machine, and tells deck its stderr as it goes and at last its stdout or failure.
func (s *Server) script(w http.ResponseWriter, r *http.Request) {
	var in scriptCall
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&in); err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !sessionName.MatchString(in.Session) || !sessionName.MatchString(in.Side) || in.Dir == "" {
		http.Error(w, "deck node: a script names its session, its folder and deck's side", http.StatusBadRequest)
		return
	}
	if err := root(append([]string{in.Dir}, in.Read...)...); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	folders, ok := s.folders(w, in.Folders)
	if !ok {
		return
	}
	done, ok := s.begin(w, in.Session)
	if !ok {
		return
	}
	defer close(done)
	dir := s.Home.Mirror(in.Session)
	at := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	tok, end := s.Sandbox.Side(s.toDeck(in.Side, ""))
	defer end()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	rc := http.NewResponseController(w)
	send := func(f scriptFrame) {
		b, _ := json.Marshal(f)
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
		_ = rc.Flush()
	}
	_ = rc.Flush()
	job := script.Job{Project: in.Project, Script: &in.Script, Source: in.Source, Dir: at(in.Dir), Stdin: in.Stdin, Token: in.Side,
		Box: sandbox.Box{Kind: sandbox.KindScript, Read: folders, Hosts: in.Hosts, GPU: in.GPU,
			Mark: s.Sandbox.Mark(sandbox.KindScript, in.Session), Deck: tok},
		OnLine: func(line string) { send(scriptFrame{Line: &line}) }}
	for _, rd := range in.Read {
		job.Read = append(job.Read, at(rd))
	}
	out, err := script.New(s.Sandbox, home.Layout(dir)).Run(r.Context(), job)
	if r.Context().Err() != nil {
		return
	}
	d := scriptDone{Out: out}
	var f *fault.F
	switch {
	case errors.As(err, &f):
		d.Fault = f
	case err != nil:
		d.Error = untrusted.Cut(err.Error(), 8000)
	}
	send(scriptFrame{Done: &d})
}

// check runs the self-check of an agent on the node.
func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	var in checkCall
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || !sessionName.MatchString(in.Side) {
		http.Error(w, "deck node: a self-check names its agent and deck's side", http.StatusBadRequest)
		return
	}
	var out check
	pv, err := s.build(in.Spec, in.Side)
	if err != nil {
		out.Error = err.Error()
	} else if sc, ok := pv.(provider.SelfChecker); ok {
		c := sc.SelfCheck(r.Context())
		out = check{Version: c.Version, Held: c.Held, ModelFailed: c.ModelFailed, Slow: c.Slow, Speed: c.Speed}
		if c.Err != nil {
			out.Error = c.Err.Error()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// login runs the sign-in of a CLI on the node: deck's call carries its stdin, the answer its output, the trailer how
// it ended.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var spec Spec
	if err := json.Unmarshal([]byte(r.URL.Query().Get("spec")), &spec); err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusBadRequest)
		return
	}
	pv, err := s.build(spec, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	si, ok := pv.(provider.SignIn)
	if !ok {
		http.Error(w, "deck node: "+spec.Agent+" is not signed in to by a link", http.StatusBadRequest)
		return
	}
	w.Header().Set("Trailer", exitTrailer)
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	var mu sync.Mutex
	out := writerFunc(func(b []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		n, err := w.Write(b)
		_ = rc.Flush()
		return n, err
	})
	p, err := si.Login(r.Context(), sandbox.IO{Stdin: r.Body, Stdout: out, Stderr: func(line string) { _, _ = out.Write([]byte(line + "\n")) }})
	if err == nil {
		err = p.Wait()
	}
	exit := "ok"
	if err != nil {
		exit = untrusted.Line(err.Error(), 500)
	}
	mu.Lock()
	w.Header().Set(exitTrailer, exit)
	mu.Unlock()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// Package models is deck's proxy of models: what a call asks, where it goes, its hops and its usage.
package models

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/registry"
)

// Policy is what a proxy of models goes by.
type Policy struct {
	// Connection is the connection of every call; empty, the first part of the path names it.
	Connection string
	// Route finds the hops of what a call asks; Allow, when set, lets the call go or says why not, its ctx holding
	// the route.
	Route func(ctx context.Context, ask Ask) ([]Hop, error)
	Allow func(ctx context.Context, ask Ask, body []byte) error
	// Named puts the model of each hop in every JSON body, not only in one that names a model.
	Named bool
	// Answer hears each answer of a call of an agent's turn, one with tools, at its head and a refusal again at its end.
	Answer func(provider.Answer)
	// Plain, once set, drops the reasoning such a call hands back, as the model refused it.
	Plain *atomic.Bool
	// Started hears the id of a job a call started, such as a video, so only its caller asks after it.
	Started func(hops []Hop, job string)
}

// Handler serves calls of models by p: it reads what each asks, routes it, lets p allow it and sends it through
// the hops, passing the answer on as it comes.
func Handler(p Policy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := registry.ReadBody(w, r)
		if err != nil {
			return
		}
		turn := p.Answer != nil && turnCall(body)
		refuse := func(err error) {
			if !turn {
				http.Error(w, err.Error(), fault.Status(err))
				return
			}
			a := &answer{w: w, heard: p.Answer}
			http.Error(a, err.Error(), fault.Status(err))
			p.Answer(a.said(a.words.String()))
		}
		connection, path := p.Connection, strings.TrimPrefix(r.URL.Path, "/")
		if connection == "" {
			connection, path, _ = strings.Cut(path, "/")
		}
		ask, err := AskOf(connection, r.Method, path, r.Header, body)
		if err != nil {
			refuse(err)
			return
		}
		hops, err := p.Route(r.Context(), ask)
		if err != nil {
			refuse(err)
			return
		}
		ctx := WithRoute(r.Context(), ask, hops)
		if p.Allow != nil {
			if err := p.Allow(ctx, ask, body); err != nil {
				refuse(err)
				return
			}
		}
		r = r.WithContext(ctx)
		replace := ask.InBody || p.Named
		if !turn && p.Started != nil && Starts(ask.Method, ask.Path) {
			j := &job{ResponseWriter: w}
			ServeHops(j, r, path, body, replace, hops)
			if id := j.id(); id != "" {
				p.Started(hops, id)
			}
			return
		}
		if !turn {
			ServeHops(w, r, path, body, replace, hops)
			return
		}
		plain := p.Plain != nil && p.Plain.Load()
		if plain {
			body = withoutReasoning(body)
		}
		a := &answer{w: w, hold: p.Plain != nil && !plain, heard: p.Answer}
		ServeHops(a, r, path, body, replace, hops)
		if a.held() && refusesReasoning(a.buf.Bytes()) {
			p.Plain.Store(true)
			a = &answer{w: w, heard: p.Answer}
			ServeHops(a, r, path, withoutReasoning(body), replace, hops)
		}
		a.release()
		if a.code >= 400 {
			p.Answer(a.said(a.words.String()))
		}
	})
}

// turnCall reports whether a call of the model is one of an agent's turn, which carries the tools of the agent, and
// not one on its side such as the title of a session; only the turn's answers tell a limit or a refused key.
func turnCall(body []byte) bool {
	var v struct {
		Tools []json.RawMessage `json:"tools"`
	}
	return json.Unmarshal(body, &v) == nil && len(v.Tools) > 0
}

// answer passes an answer of a model on and keeps what it said; with hold, a bad request waits, since deck may send
// it again.
type answer struct {
	w      http.ResponseWriter
	hold   bool
	header http.Header
	code   int
	buf    bytes.Buffer
	words  strings.Builder
	heard  func(provider.Answer)
}

func (a *answer) Header() http.Header {
	if a.header == nil {
		a.header = http.Header{}
	}
	return a.header
}

func (a *answer) WriteHeader(code int) {
	if a.code != 0 {
		return
	}
	a.code = code
	if !a.held() {
		a.pass()
	}
}

func (a *answer) Write(b []byte) (int, error) {
	if a.code == 0 {
		a.WriteHeader(http.StatusOK)
	}
	if a.code >= 400 && a.words.Len() < 2000 {
		a.words.Write(b[:min(len(b), 2000-a.words.Len())])
	}
	if a.held() {
		return a.buf.Write(b)
	}
	return a.w.Write(b)
}

func (a *answer) Flush() {
	if f, ok := a.w.(http.Flusher); ok && !a.held() {
		f.Flush()
	}
}

// held reports whether the answer is a bad request kept back.
func (a *answer) held() bool { return a.hold && badRequest(a.code) }

func (a *answer) pass() {
	for k, v := range a.header {
		a.w.Header()[k] = v
	}
	a.heard(a.said(""))
	a.w.WriteHeader(a.code)
}

// release hands a kept bad request on.
func (a *answer) release() {
	if !a.held() {
		return
	}
	a.pass()
	_, _ = a.w.Write(a.buf.Bytes())
}

// said is the answer with the words of its refusal.
func (a *answer) said(words string) provider.Answer {
	return provider.Answer{Status: a.code, Header: a.Header().Clone(), Past: a.Header().Get(provider.PastLimit) != "", Words: words}
}

// echoed are the fields of reasoning an agent hands back in the messages of its history, which no OpenAI-compatible
// API needs and a strict one refuses.
var echoed = []string{"reasoning_content", "reasoning", "reasoning_details"}

// refusesReasoning reports whether the model API refused a call naming a field of reasoning, quoted as a field.
func refusesReasoning(answer []byte) bool {
	text := strings.ReplaceAll(string(answer), `\"`, `"`)
	return slices.ContainsFunc(echoed, func(f string) bool {
		return slices.ContainsFunc([]string{"'", `"`, "`"}, func(q string) bool { return strings.Contains(text, q+f+q) })
	})
}

// badRequest is an answer of the model API that refuses what the call holds.
func badRequest(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusUnprocessableEntity
}

// withoutReasoning is a call of the model with no field of reasoning in its messages.
func withoutReasoning(body []byte) []byte {
	var call map[string]json.RawMessage
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(body, &call) != nil || json.Unmarshal(call["messages"], &msgs) != nil {
		return body
	}
	for _, m := range msgs {
		for _, f := range echoed {
			delete(m, f)
		}
	}
	call["messages"], _ = json.Marshal(msgs)
	out, err := json.Marshal(call)
	if err != nil {
		return body
	}
	return out
}

// Starts reports whether a request starts a job a model runs on, such as a video, that is asked after later.
func Starts(method, path string) bool {
	return method == http.MethodPost && strings.Trim(path, "/") == "videos"
}

// job passes an answer on and keeps its head, where a started job says its id.
type job struct {
	http.ResponseWriter
	code int
	head bytes.Buffer
}

func (j *job) WriteHeader(code int) {
	j.code = code
	j.ResponseWriter.WriteHeader(code)
}

func (j *job) Write(b []byte) (int, error) {
	if j.head.Len() < 64<<10 {
		j.head.Write(b[:min(len(b), 64<<10-j.head.Len())])
	}
	return j.ResponseWriter.Write(b)
}

func (j *job) Flush() {
	if f, ok := j.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// id is the id of the job a successful answer names.
func (j *job) id() string {
	var v struct {
		ID string `json:"id"`
	}
	if j.code >= 300 || json.Unmarshal(j.head.Bytes(), &v) != nil {
		return ""
	}
	return v.ID
}

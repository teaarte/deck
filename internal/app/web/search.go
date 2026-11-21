package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/egress"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

type webSearchIn struct {
	Query string `desc:"What to search the web for"`
	Count int64  `opt:"" desc:"How many results, 10 when left out, at most 20"`
}

type searchView struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// found is what a search answered: at most maxResults results and the service that gave them.
type found struct {
	at      time.Time
	search  string
	results []searchView
}

const (
	maxResults = 20
	// searching is how many requests a search connection of a workspace gets at once; searchFor the most a search
	// takes; cacheFor how long a query is answered by its last results.
	searching = 2
	searchFor = 30 * time.Second
	// slotWait is how long a search waits for a busy connection before it goes to the next.
	slotWait = 10 * time.Second
	// configFor is the most a read of the /config of a SearXNG takes; engineFor how long deck keeps what it said.
	configFor = 5 * time.Second
	engineFor = 5 * time.Minute
	cacheFor  = 10 * time.Minute
	cacheMax  = 500
)

// webSearch asks the search services assigned to search, the fallbacks after the first, or answers a query asked
// of the same services in the last minutes as then; the journal keeps the query, the service and each result.
func (s *Service) webSearch(ctx context.Context, act registry.Actor, in webSearchIn) (got any, err error) {
	read := map[string]any{"query": in.Query}
	defer func() { got = lineOf(act, got, read) }()
	if strings.TrimSpace(in.Query) == "" {
		return nil, registry.Invalid("a search needs a query")
	}
	count := int(in.Count)
	if count <= 0 {
		count = 10
	}
	hops, err := s.searches(ctx, act)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprint(act.Workspace)
	for _, h := range hops {
		key += "\x00" + h.Name
	}
	key += "\x00\x00" + in.Query
	f, ok := s.cached(key)
	if ok {
		read["cached"] = true
	} else {
		if f, err = s.search(ctx, act, hops, in.Query); err != nil {
			return nil, err
		}
		s.keep(key, f)
	}
	read["search"] = f.search
	out, urls := f.results[:min(count, len(f.results))], []string{}
	for _, r := range out {
		urls = append(urls, r.URL)
	}
	read["results"] = urls
	return map[string]any{"results": out, "search": f.search}, nil
}

// searches are the services of search of act in order, the local or free ones of a trial.
func (s *Service) searches(ctx context.Context, act registry.Actor) ([]models.Hop, error) {
	hops, err := s.Route(ctx, act, models.Ask{Connection: "@", Model: "@" + models.KindSearch})
	if err != nil {
		return nil, err
	}
	if act.Trial {
		hops = slices.DeleteFunc(hops, func(h models.Hop) bool { return !h.Local && !h.Free })
		if len(hops) == 0 {
			return nil, registry.Refuse("trial run: no search of deck's settings is local or free, so a trial does not search")
		}
	}
	for i := range hops {
		done := hops[i].Done
		hops[i].Done = func(ctx context.Context, status int, u *provider.Usage) {
			if u == nil && status > 0 && status < 400 {
				u = &provider.Usage{}
			}
			done(ctx, status, u)
		}
		hops[i].Target.Transport = slotted{next: hops[i].Target.Transport, slots: s.slots(fmt.Sprint(act.Workspace, "\x00", hops[i].Connection))}
	}
	return hops, nil
}

// search asks the services of search in order within searchFor; one that answers no results because none of its
// engines answered is red and hands the query on.
func (s *Service) search(ctx context.Context, act registry.Actor, hops []models.Hop, query string) (found, error) {
	ctx, cancel := context.WithTimeout(ctx, searchFor)
	defer cancel()
	q := url.Values{"q": {query}, "format": {"json"}}.Encode()
	f, _, err := models.ReadHops(ctx, hops, func(models.Hop) (models.Call, error) {
		return models.Call{Method: http.MethodGet, Path: "search", Query: q, Header: http.Header{"Accept": {"application/json"}}}, nil
	}, func(h models.Hop, resp *http.Response) (found, error) { return s.answer(ctx, act, h, resp) })
	return f, err
}

// answer reads what a search answered; no results while none of its engines answered is a search that did not answer.
func (s *Service) answer(ctx context.Context, act registry.Actor, h models.Hop, resp *http.Response) (found, error) {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	// The body closes first, since its close tells the health of the search the answer.
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return found{}, fault.New(fault.ModelUnavailable, fmt.Sprintf("deck: search %s answered %s: %s", h.Name, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500)))
	}
	var v struct {
		Results      []struct{ Title, URL, Content string }
		Unresponsive [][]string `json:"unresponsive_engines"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return found{}, fault.New(fault.ModelUnavailable, fmt.Sprintf("deck: search %s answered what is not JSON; SearXNG needs json in search.formats of its settings: %s", h.Name, untrusted.Cut(string(raw), 300)))
	}
	if len(v.Results) == 0 && len(v.Unresponsive) > 0 && s.allDown(ctx, act, h, v.Unresponsive) {
		engines := make([]string, 0, len(v.Unresponsive))
		for _, e := range v.Unresponsive {
			switch {
			case len(e) > 1:
				engines = append(engines, fmt.Sprintf("%s (%s)", untrusted.Flat(e[0], 60), untrusted.Flat(e[1], 100)))
			case len(e) == 1:
				engines = append(engines, untrusted.Flat(e[0], 60))
			}
		}
		text := "answered no results, its engines did not answer: " + strings.Join(engines, ", ")
		said := fmt.Sprintf("deck: search %s %s", h.Name, text)
		if err := s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			_, err := health.Set(ctx, q, s.St.Now(), health.Of{Workspace: act.Workspace}, health.Model(h.Name), health.Error, text)
			return err
		}); err != nil {
			said += fmt.Sprintf("; its health was not kept: %v", err)
		}
		return found{}, models.Unread(said)
	}
	f := found{at: s.St.Now(), search: h.Name, results: []searchView{}}
	for _, r := range v.Results[:min(len(v.Results), maxResults)] {
		f.results = append(f.results, searchView{Title: untrusted.Cut(r.Title, 300), URL: r.URL, Snippet: untrusted.Cut(r.Content, 1000)})
	}
	return f, nil
}

// allDown says every web engine the SearXNG of h asks did not answer; one that names no such engine is not down.
func (s *Service) allDown(ctx context.Context, act registry.Actor, h models.Hop, unresponsive [][]string) bool {
	web := s.webEngines(ctx, fmt.Sprint(act.Workspace, "\x00", h.Connection), h)
	return len(web) > 0 && !slices.ContainsFunc(web, func(name string) bool {
		return !slices.ContainsFunc(unresponsive, func(u []string) bool { return len(u) > 0 && u[0] == name })
	})
}

// webEngines are the engines a SearXNG asks for a plain query by its /config, kept for engineFor: the enabled ones
// of general that are web too, since those also of translate or currency answer only queries of their own pattern.
func (s *Service) webEngines(ctx context.Context, key string, h models.Hop) []string {
	s.mu.Lock()
	k, ok := s.engines[key]
	s.mu.Unlock()
	if ok && s.St.Now().Sub(k.at) < engineFor {
		return k.names
	}
	ctx, cancel := context.WithTimeout(ctx, configFor)
	defer cancel()
	resp, err := models.Send(ctx, h.Target, models.Call{Method: http.MethodGet, Path: "config", Header: http.Header{"Accept": {"application/json"}}})
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var c struct {
		Engines []struct {
			Name       string
			Categories []string
			Enabled    bool
		}
	}
	if resp.StatusCode >= 400 || json.NewDecoder(io.LimitReader(resp.Body, maxPage)).Decode(&c) != nil {
		return nil
	}
	names := []string{}
	for _, e := range c.Engines {
		if e.Enabled && slices.Contains(e.Categories, "general") && slices.Contains(e.Categories, "web") {
			names = append(names, e.Name)
		}
	}
	s.mu.Lock()
	if s.engines == nil {
		s.engines = map[string]engines{}
	}
	s.engines[key] = engines{at: s.St.Now(), names: names}
	s.mu.Unlock()
	return names
}

// engines are the web engines of a SearXNG as it said at.
type engines struct {
	at    time.Time
	names []string
}

// slotted is the transport of a search connection that sends searching requests at once; a slot is free again once
// the answer's body is closed.
type slotted struct {
	next  http.RoundTripper
	slots chan struct{}
}

func (t slotted) RoundTrip(r *http.Request) (*http.Response, error) {
	wait := time.NewTimer(slotWait)
	defer wait.Stop()
	select {
	case t.slots <- struct{}{}:
	case <-wait.C:
		return nil, egress.Held
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	resp, err := t.next.RoundTrip(r)
	if err != nil {
		<-t.slots
		return nil, err
	}
	resp.Body = &freeing{ReadCloser: resp.Body, free: sync.OnceFunc(func() { <-t.slots })}
	return resp, nil
}

type freeing struct {
	io.ReadCloser
	free func()
}

func (b *freeing) Close() error {
	defer b.free()
	return b.ReadCloser.Close()
}

// slots are the slots of a search connection of a workspace by key.
func (s *Service) slots(key string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slot == nil {
		s.slot = map[string]chan struct{}{}
	}
	if s.slot[key] == nil {
		s.slot[key] = make(chan struct{}, searching)
	}
	return s.slot[key]
}

// cached is what a search answered key less than cacheFor ago.
func (s *Service) cached(key string) (found, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.cache[key]
	if !ok || s.St.Now().Sub(f.at) >= cacheFor {
		return found{}, false
	}
	return f, true
}

// keep keeps what a search answered key, dropping what is past cacheFor, and entries of its own choice while the
// cache is full.
func (s *Service) keep(key string, f found) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.St.Now()
	for k, old := range s.cache {
		if now.Sub(old.at) >= cacheFor || len(s.cache) >= cacheMax {
			delete(s.cache, k)
		}
	}
	if s.cache == nil {
		s.cache = map[string]found{}
	}
	s.cache[key] = f
}

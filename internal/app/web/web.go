// Package web keeps the web of deck's agents: a search through the search service of deck's settings and a read of
// a page of the hosts a module holds the network of.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/net/html"

	"deck2/internal/docs"
	"deck2/internal/egress"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/modelproxy"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/untrusted"
)

// Service reads the web for deck's agents; the gateway journals each read.
type Service struct {
	St *store.Store
	// Route finds where an actor's call of a model goes, the search of deck's settings for @search.
	Route func(ctx context.Context, act registry.Actor, ask modelproxy.Ask) ([]modelproxy.Hop, error)

	mu      sync.Mutex
	slot    map[string]chan struct{}
	engines map[string]engines
	cache   map[string]found
}

// Commands are the web of deck's agents.
func (s *Service) Commands() []*registry.Command { return s.webCommands() }

const (
	maxPage     = 4 << 20
	maxPageText = 100 << 10
)

// webDomains are the sites the caller reads pages of: any public one for the owner, the hosts its module holds the
// network of for the rest, the master by its own right as any module.
func (s *Service) webDomains(ctx context.Context, act registry.Actor) ([]string, error) {
	if act.Kind == registry.Owner {
		return []string{"*"}, nil
	}
	if act.ProjectID == 0 {
		return nil, registry.Refuse("only a module reads pages through deck by its right network; an automation token reads the web itself")
	}
	m, g, err := docs.Rights(ctx, s.St.Q, act.ProjectID)
	if err != nil {
		return nil, err
	}
	return m.Held(g, manifest.Network, manifest.Programs), nil
}

// pageNeeds is the right a read of a page needs: the network of its programs on the host of its address, which its
// module declares.
func (s *Service) pageNeeds(ctx context.Context, act registry.Actor, _ registry.Subject, input json.RawMessage) ([]registry.Need, error) {
	var in webFetchIn
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, registry.Invalid("%v", err)
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, registry.Invalid("%q is no address of a page: give an http or https URL", in.URL)
	}
	if act.ProjectID == 0 {
		return nil, registry.Refuse("only a module reads pages through deck by its right network; an automation token reads the web itself")
	}
	n := registry.Need{Right: manifest.Network, Who: manifest.Programs, Subject: strings.ToLower(u.Hostname())}
	m, _, err := docs.Rights(ctx, s.St.Q, act.ProjectID)
	if err != nil {
		return nil, err
	}
	if !m.Declares(n) {
		return nil, registry.Refuse("%s is not of the hosts its module holds the network of; an agent reads the pages of the hosts its module holds the network of, which it declares in rights", n.Subject)
	}
	return []registry.Need{n}, nil
}

type webFetchIn struct {
	URL string `desc:"Address of the page, http or https"`
}

// webFetch reads a page of the caller's sites as text: the words of an HTML page, a text or JSON one as it is; the
// journal keeps the address asked and the one read after redirects.
func (s *Service) webFetch(ctx context.Context, act registry.Actor, in webFetchIn) (got any, err error) {
	read := map[string]any{"url": in.URL}
	defer func() { got = lineOf(act, got, read) }()
	domains, err := s.webDomains(ctx, act)
	if err != nil {
		return nil, err
	}
	resp, err := egress.Fetch(ctx, domains, in.URL)
	if err != nil {
		return nil, registry.Refuse("%v; an agent reads the pages of the hosts its module holds the network of", err)
	}
	defer resp.Body.Close()
	if at := resp.Request.URL.String(); at != in.URL {
		read["read"] = at
	}
	if resp.StatusCode >= 400 {
		return nil, fault.New(fault.Network, fmt.Sprintf("deck: %s answered %s", in.URL, resp.Status))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if err != nil {
		return nil, err
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	title, text := "", string(body)
	switch {
	case ct == "text/html" || ct == "application/xhtml+xml" || ct == "" && strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<html"):
		title, text = pageText(strings.NewReader(text))
	case strings.HasPrefix(ct, "text/") || strings.HasSuffix(ct, "json") || strings.HasSuffix(ct, "xml") || ct == "" && utf8.Valid(body):
	default:
		return nil, registry.Invalid("%s is %s, not a page of text", in.URL, ct)
	}
	return map[string]any{"url": resp.Request.URL.String(), "title": untrusted.Cut(title, 300), "text": untrusted.Cut(text, maxPageText),
		"truncated": len(text) > maxPageText}, nil
}

// lineOf is the result of a read of the web with its own line of the journal: what it read, done or rejected, with
// the host it read as the subject of the network; a read cut short keeps its line too.
func lineOf(act registry.Actor, out any, read map[string]any) registry.Result {
	raw, _ := json.Marshal(read)
	n := registry.Need{Right: manifest.Network, Who: manifest.Agents}
	if u, ok := read["url"].(string); ok {
		if at, err := url.Parse(u); err == nil {
			n.Subject = at.Hostname()
		}
	}
	return registry.Result{Data: out, Line: &registry.Line{Need: n, Params: string(raw), Project: act.ProjectID}}
}

// skipped are the elements whose content is no text of a page.
var skipped = map[string]bool{"script": true, "style": true, "noscript": true, "template": true, "svg": true}

// blocks are the elements that start a line of their own.
var blocks = map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "section": true, "article": true, "pre": true, "blockquote": true, "td": true}

// pageText is the title and the words of an HTML page, a line per block.
func pageText(r io.Reader) (string, string) {
	z := html.NewTokenizer(r)
	var title, b strings.Builder
	skip, inTitle := 0, false
	for {
		switch z.Next() {
		case html.ErrorToken:
			var lines []string
			for _, l := range strings.Split(b.String(), "\n") {
				if l = strings.Join(strings.Fields(l), " "); l != "" {
					lines = append(lines, l)
				}
			}
			return strings.Join(strings.Fields(title.String()), " "), strings.Join(lines, "\n")
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch n := string(name); {
			case skipped[n]:
				skip++
			case n == "title":
				inTitle = true
			case blocks[n]:
				b.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch n := string(name); {
			case skipped[n] && skip > 0:
				skip--
			case n == "title":
				inTitle = false
			case blocks[n]:
				b.WriteString("\n")
			}
		case html.TextToken:
			switch {
			case inTitle:
				title.Write(z.Text())
			case skip == 0:
				b.Write(z.Text())
				b.WriteString(" ")
			}
		}
	}
}

// webCommands are the web of deck's agents, whose boxes reach only their rights; each writes its line of the journal
// with the addresses it read.
func (s *Service) webCommands() []*registry.Command {
	fetch := registry.Def("web_fetch", "Read a page",
		"Reads a web page as text: the words of an HTML page, a text or JSON one as it is; an agent of a module reads only the hosts its module holds the network of (\"*\" for any).",
		registry.Everyone, s.webFetch).Brings(registry.FromOutside)
	fetch.Gate.Needs = s.pageNeeds
	return []*registry.Command{
		registry.Def("web_search", "Search the web",
			"Searches the web through the search service of deck's settings and gives titles, addresses and snippets.",
			registry.Everyone, s.webSearch).Brings(registry.FromOutside).Stays(),
		fetch,
	}
}

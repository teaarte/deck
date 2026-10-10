package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// SearXNGInstall is how the owner runs SearXNG on this machine with its JSON answers on, for the search of deck.
const SearXNGInstall = `mkdir -p ~/searxng && printf 'use_default_settings: true\nserver:\n  secret_key: "%s"\nsearch:\n  formats: [html, json]\n' "$(openssl rand -hex 32)" > ~/searxng/settings.yml && ` +
	`docker run -d --name searxng --restart always -p 127.0.0.1:8888:8080 -v ~/searxng:/etc/searxng searxng/searxng`

// noSearch is what the owner does when no search is assigned.
const noSearch = "no search is set up, so agents without their own web only read pages; run SearXNG (" + SearXNGInstall +
	"), add it in Settings → Connections (api raw, url http://127.0.0.1:8888, local), as a model of kind search in Settings → Models, and assign it to search"

const (
	maxPage     = 4 << 20
	maxPageText = 100 << 10
)

// webDomains are the sites the caller reads pages of: any public one for the owner side, the read domains in force
// of the project for the rest; a project with no_network has no web.
func (s *Service) webDomains(ctx context.Context, act registry.Actor) ([]string, error) {
	if act.OwnerSide() || act.ProjectID == 0 {
		return []string{"*"}, nil
	}
	m, err := docs.For(ctx, s.St.Q, act, act.ProjectID)
	if err != nil {
		return nil, err
	}
	if m.NoNetwork {
		p, _ := s.St.Q.GetProject(ctx, act.ProjectID)
		return nil, registry.Refuse("deck: project %s has no_network, so its agents have no web", p.Name)
	}
	return m.Domains.Read, nil
}

type webSearchIn struct {
	Query string `desc:"What to search the web for"`
	Count int64  `opt:"" desc:"How many results, 10 when left out, at most 20"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// webSearch asks the search services assigned to search, the fallbacks after the first.
func (s *Service) webSearch(ctx context.Context, act registry.Actor, in webSearchIn) (any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, registry.Invalid("deck: a search needs a query")
	}
	if _, err := s.webDomains(ctx, act); err != nil {
		return nil, err
	}
	hops, err := s.Route(ctx, act, gateway.ModelAsk{Connection: "@", Model: "@" + RoleSearch})
	if err != nil {
		return nil, err
	}
	if act.Trial {
		hops = slices.DeleteFunc(hops, func(h gateway.ModelHop) bool { return !h.Local && !h.Free })
		if len(hops) == 0 {
			return nil, registry.Refuse("deck: trial run: no search of deck's settings is local or free, so a trial does not search")
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
	}
	query := url.Values{"q": {in.Query}, "format": {"json"}}.Encode()
	resp, h, err := gateway.SendHops(ctx, hops, func(gateway.ModelHop) (gateway.ModelCall, error) {
		return gateway.ModelCall{Method: http.MethodGet, Path: "search", Query: query, Header: http.Header{"Accept": {"application/json"}}}, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deck: search %s answered %s: %s", h.Name, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))
	}
	var v struct {
		Results []struct{ Title, URL, Content string }
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("deck: search %s answered what is not JSON; SearXNG needs json in search.formats of its settings: %s", h.Name, untrusted.Cut(string(raw), 300))
	}
	count := int(in.Count)
	if count <= 0 {
		count = 10
	}
	out := []searchResult{}
	for _, r := range v.Results {
		if len(out) == min(count, 20) {
			break
		}
		out = append(out, searchResult{Title: untrusted.Cut(r.Title, 300), URL: r.URL, Snippet: untrusted.Cut(r.Content, 1000)})
	}
	return map[string]any{"results": out, "search": h.Name}, nil
}

type webFetchIn struct {
	URL string `desc:"Address of the page, http or https"`
}

// webFetch reads a page of the caller's sites as text: the words of an HTML page, a text or JSON one as it is.
func (s *Service) webFetch(ctx context.Context, act registry.Actor, in webFetchIn) (any, error) {
	domains, err := s.webDomains(ctx, act)
	if err != nil {
		return nil, err
	}
	resp, err := gateway.Fetch(ctx, domains, in.URL)
	if err != nil {
		return nil, registry.Refuse("deck: %v; an agent reads the pages of its project's domains.read", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deck: %s answered %s", in.URL, resp.Status)
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
		return nil, registry.Invalid("deck: %s is %s, not a page of text", in.URL, ct)
	}
	return map[string]any{"url": resp.Request.URL.String(), "title": untrusted.Cut(title, 300), "text": untrusted.Cut(text, maxPageText),
		"truncated": len(text) > maxPageText}, nil
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

// webCommands are the web of deck for the agents that have none of their own.
func (s *Service) webCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("web_search", "Search the web",
			"Searches the web through the search service of deck's settings and gives titles, addresses and snippets; for an agent without a web search of its own. Not in a project with no_network.",
			registry.Everyone, s.webSearch),
		registry.Def("web_fetch", "Read a page",
			"Reads a web page as text: the words of an HTML page, a text or JSON one as it is; an agent of a project reads only the sites of its domains.read (\"*\" for any). Not in a project with no_network.",
			registry.Everyone, s.webFetch),
	}
}

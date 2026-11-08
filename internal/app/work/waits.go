package work

import (
	"cmp"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"deck2/internal/egress"
	"deck2/internal/health"
	"deck2/internal/models"
)

// waitView is a thing of the models that waits for the owner, with what its one button needs: a sign-in, a model
// that is down under some work, a connection without its key, a node that is away, an agent not installed.
type waitView struct {
	Kind  string `json:"kind" enum:"sign_in|down|key|node|install"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Icon  string `json:"icon"`
	Tone  string `json:"tone" enum:"warn|error"`
	// Logo names the model or provider it is about.
	Logo string `json:"logo,omitempty"`
	// Agent is the agent to sign in to or install; Connection the one without its key.
	Agent      string          `json:"agent,omitempty"`
	Connection *connectionView `json:"connection,omitempty"`
	// Model is a model that is down under the work of Role, Choices and Groups the models that could take it, Why its
	// health; Places the work it leaves, or the connections a node away serves.
	Model   string   `json:"model,omitempty"`
	Role    string   `json:"role,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Groups  []string `json:"groups,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	Logos   []string `json:"logos,omitempty"`
	Why     string   `json:"why,omitempty"`
	Places  []string `json:"places,omitempty"`
}

// waits are what of the models waits for the owner: agents to sign in to or install, work whose first model is down,
// connections of models without their key, and nodes away.
func waits(out *modelsView, tr func(string) string) []waitView {
	w := []waitView{}
	// An agent is used when work goes to it or to a model that runs in it.
	used := map[string]bool{}
	for _, m := range out.Models {
		if m.Used {
			used[m.Name], used[m.Agent] = true, true
		}
	}
	login := map[string]bool{}
	for _, a := range out.Agents {
		switch {
		case a.Status == health.NeedsLogin && used[a.Name]:
			login[a.Name] = true
			w = append(w, waitView{Kind: "sign_in", Title: fmt.Sprintf(tr("Sign in to %s"), a.Name), Text: tr("Agent turns of the modules wait for it"),
				Icon: "login", Tone: "warn", Logo: a.Name, Agent: a.Name})
		case a.Status == health.Missing && used[a.Name]:
			w = append(w, waitView{Kind: "install", Title: fmt.Sprintf(tr("Install %s"), a.Name), Text: a.Error, Icon: "download", Tone: "warn", Logo: a.Name, Agent: a.Name})
		}
	}
	type downKey struct{ model, role string }
	down := map[downKey]int{}
	groups, notes, logos := map[string]string{}, map[string]string{}, map[string]string{}
	for _, v := range out.Work {
		if v.Why == "" || len(v.Choices) == 0 {
			continue
		}
		model, _, _ := strings.Cut(v.Why, ": ")
		if login[model] {
			continue
		}
		place := cmp.Or(v.ProjectTitle, "deck") + " · " + v.Title
		// The choices are those every work of the group could take.
		k := downKey{model, v.Role}
		if i, ok := down[k]; ok {
			w[i].Places = append(w[i].Places, place)
			w[i].Choices = slices.DeleteFunc(w[i].Choices, func(n string) bool { return !slices.Contains(v.Choices, n) })
			continue
		}
		down[k] = len(w)
		w = append(w, waitView{Kind: "down", Title: fmt.Sprintf(tr("%s does not work"), model), Icon: "alert-triangle", Tone: "error", Logo: model,
			Model: model, Role: v.Role, Choices: slices.DeleteFunc(slices.Clone(v.Choices), func(n string) bool { return n == model }),
			Why: v.Why, Places: []string{place}})
		for i, n := range v.Choices {
			if i < len(v.ChoiceGroups) {
				groups[n] = v.ChoiceGroups[i]
			}
			if i < len(v.ChoiceNotes) && i < len(v.ChoiceLogos) {
				notes[n], logos[n] = v.ChoiceNotes[i], v.ChoiceLogos[i]
			}
		}
	}
	for i := range w {
		if d := &w[i]; d.Kind == "down" {
			d.Text = fmt.Sprintf(tr("Waits for it: %s"), strings.Join(d.Places, "; "))
			d.Groups, d.Notes, d.Logos = nil, nil, nil
			for _, n := range d.Choices {
				d.Groups, d.Notes, d.Logos = append(d.Groups, groups[n]), append(d.Notes, notes[n]), append(d.Logos, cmp.Or(logos[n], n))
			}
		}
	}
	// A model down with no other that could take all its work waits in Who does what, a work at a time.
	w = slices.DeleteFunc(w, func(d waitView) bool { return d.Kind == "down" && len(d.Choices) == 0 })
	away := map[string]int{}
	for i := range out.Connections {
		c := out.Connections[i]
		switch {
		case c.Models == 0 || c.API == models.APICLI:
		case c.Node != "" && !c.Online:
			if i, ok := away[c.Node]; ok {
				w[i].Places = append(w[i].Places, c.Name)
				continue
			}
			away[c.Node] = len(w)
			w = append(w, waitView{Kind: "node", Title: fmt.Sprintf(tr("Node %s is away"), c.Node), Icon: "plug-connected-x", Tone: "warn",
				Places: []string{c.Name}})
		case c.Key == "none" && keyed(c):
			w = append(w, waitView{Kind: "key", Title: fmt.Sprintf(tr("Add the key of %s"), c.Name), Text: tr("Its models answer only with an API key"),
				Icon: "key", Tone: "warn", Logo: c.Name, Connection: &c})
		}
	}
	for i := range w {
		if a := &w[i]; a.Kind == "node" {
			a.Text = fmt.Sprintf(tr("Its models wait for it: %s. Wake the machine, or start deck-node there"), strings.Join(a.Places, ", "))
		}
	}
	slices.SortStableFunc(w, func(a, b waitView) int { return toneRank(a.Tone) - toneRank(b.Tone) })
	return w
}

func toneRank(t string) int {
	if t == "error" {
		return 0
	}
	return 1
}

// keyed says a connection speaks an API that takes a key at a public address; a server of the owner's own machine,
// network or node takes none.
func keyed(c connectionView) bool {
	if c.Local || c.Node != "" || c.API == "raw" {
		return false
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if egress.IsLoopback(host) || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	return true
}

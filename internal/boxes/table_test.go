package boxes

import (
	"fmt"
	"slices"
	"testing"

	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

func TestFor(t *testing.T) {
	declare := func(rights string) *manifest.Manifest {
		m, err := manifest.Parse([]byte(fmt.Sprintf(`{"version": %d, "rights": [%s]}`, manifest.FormatVersion, rights)))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	always := func(right, who string, subjects ...string) manifest.Grants {
		var g manifest.Grants
		for _, s := range subjects {
			g = append(g, manifest.Grant{Right: right, Who: who, Subject: s, Answer: manifest.Always})
		}
		return g
	}
	net := `{"right": "network", "what": ["a.example", "b.example"], "why": "w"}`
	agentNet := `{"right": "network", "who": "agent", "what": ["c.example"], "why": "w"}`
	others := `{"right": "data", "what": ["other"], "why": "w"}`
	gpu := `{"right": "files", "what": ["gpu"], "why": "w"}`
	both := append(always(manifest.Network, manifest.Programs, "a.example", "b.example"), always(manifest.Network, manifest.Agents, "c.example")...)
	for _, row := range []struct {
		kind    string
		rights  string
		granted manifest.Grants
		login   bool
		hosts   []string
		gpu     bool
		what    string
	}{
		{sandbox.KindScript, net, always(manifest.Network, manifest.Programs, "a.example"), false, []string{"a.example"}, false, "a script reaches the hosts granted of those declared"},
		{sandbox.KindScript, net, nil, false, nil, false, "a declaration alone reaches nothing"},
		{sandbox.KindScript, `{"right": "network", "what": ["a.example"], "why": "w"}`, always(manifest.Network, manifest.Programs, "a.example", "z.example"), false, []string{"a.example"}, false, "a grant without a declaration reaches nothing"},
		{sandbox.KindMCP, net, always(manifest.Network, manifest.Programs, "a.example", "b.example"), false, []string{"a.example", "b.example"}, false, "an MCP server reaches what scripts reach"},
		{sandbox.KindScript, net + "," + agentNet, both, false, []string{"a.example", "b.example"}, false, "a script never gets the network of the agents"},
		{sandbox.KindAgent, net + "," + agentNet, both, false, []string{"c.example"}, false, "an agent gets only the network declared for agents"},
		{sandbox.KindAgent, net, always(manifest.Network, manifest.Programs, "a.example"), false, nil, false, "an agent never gets the network of the programs"},
		{sandbox.KindAgent, agentNet, always(manifest.Network, manifest.Agents, "c.example"), true, nil, false, "an agent on a subscription login gets no network in its shell"},
		{sandbox.KindAgent, agentNet + "," + others, append(always(manifest.Network, manifest.Agents, "c.example"), always(manifest.Data, "", "other")...), false, nil, false, "nor one of a module that reads the data of others"},
		{sandbox.KindAgent, agentNet + "," + others, always(manifest.Network, manifest.Agents, "c.example"), false, nil, false, "a declared read of others shuts it, granted or not"},
		{sandbox.KindScript, `{"right": "network", "what": ["a.example"], "why": "w"}` + "," + others, append(always(manifest.Network, manifest.Programs, "a.example"), always(manifest.Data, "", "other")...), false, []string{"a.example"}, false, "programs keep their network beside the data of others"},
		{sandbox.KindScript, gpu, always(manifest.Files, "", "gpu"), false, nil, true, "the graphics card granted"},
		{sandbox.KindScript, gpu, nil, false, nil, false, "the graphics card declared only"},
		{sandbox.KindScript, "", always(manifest.Files, "", "gpu"), false, nil, false, "the graphics card granted, not declared"},
		{sandbox.KindAgent, `{"right": "network", "who": "agent", "what": ["*"], "why": "w"}`, always(manifest.Network, manifest.Agents, "*"), false, []string{"*"}, false, "an agent off a login with the whole internet"},
	} {
		b := For(row.kind, declare(row.rights), row.granted, row.login, []string{"/folder"})
		if !slices.Equal(b.Hosts, row.hosts) || b.GPU != row.gpu || !slices.Equal(b.Read, []string{"/folder"}) {
			t.Errorf("%s: For(%s, %s, login %v) = hosts %q, gpu %v, read %q; want %q, %v", row.what, row.kind, row.rights, row.login, b.Hosts, b.GPU, b.Read, row.hosts, row.gpu)
		}
	}
	g := append(always(manifest.Network, manifest.Agents, "c.example"), always(manifest.Data, "", "lookup")...)
	if b := For(sandbox.KindAgent, declareTool(t), g, false, nil); !slices.Equal(b.Hosts, []string{"c.example"}) {
		t.Errorf("a service of its own a module reads is no data of others: %q", b.Hosts)
	}
	if b := For(sandbox.KindBrowser, nil, nil, true, []string{"/folder"}); !slices.Equal(b.Hosts, []string{"*"}) || b.Read != nil {
		t.Errorf("the browser reaches every site and reads no folder: %q %q", b.Hosts, b.Read)
	}
}

// declareTool is a module whose agents reach c.example and whose own tool reads a service with a key.
func declareTool(t *testing.T) *manifest.Manifest {
	m, err := manifest.Parse([]byte(fmt.Sprintf(`{"version": %d,
  "tools": {"http": [{"name": "lookup", "description": "d", "method": "GET", "url": "https://api.example/x", "headers": {"K": "$secret.k"}}]},
  "rights": [{"right": "network", "who": "agent", "what": ["c.example"], "why": "w"}, {"right": "data", "what": ["lookup"], "why": "w"}]}`, manifest.FormatVersion)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

package manifest

import (
	"encoding/json"
	"testing"
)

func TestToolRight(t *testing.T) {
	for _, row := range []struct {
		method, url, headers, auth, named string
		right, subject                    string
		what                              string
	}{
		{"GET", "https://api.example/x?q={q}", "", "", "", Network, "api.example", "a GET without a key: the network of its host"},
		{"HEAD", "https://API.Example:8443/x", "", "", "", Network, "api.example", "a HEAD without a key, its host lower case without the port"},
		{"GET", "https://api.example/x", `{"X-Key": "$secret.k"}`, "", "", Data, "t", "a GET with a key in a header: the data of the service"},
		{"GET", "https://api.example/x?key=$secret.k", "", "", "", Data, "t", "a GET with a key in the URL"},
		{"GET", "$secret.link", "", "", "", Data, "t", "a GET of a private link"},
		{"GET", "https://api.example/x", "", `{"type": "secret", "secret": "k"}`, "", Data, "t", "a GET with auth"},
		{"POST", "https://api.example/x", "", "", "", Send, "t", "a POST: sending in the owner's name"},
		{"PUT", "https://api.example/x", `{"X-Key": "$secret.k"}`, "", "", Send, "t", "a PUT with a key"},
		{"DELETE", "https://api.example/x", "", "", "", Send, "t", "a DELETE"},
		{"POST", "https://api.example/search", `{"X-Key": "$secret.k"}`, "", Data, Data, "t", "a POST its author names a read of the service"},
		{"POST", "https://api.example/search", "", "", Network, Network, "api.example", "a POST without a key its author names a read of the network"},
		{"POST", "https://api.example/search", `{"X-Key": "$secret.k"}`, "", Network, Data, "t", "a keyed one named network is still the data of the service"},
		{"GET", "https://api.example/x", "", "", Send, Send, "t", "a GET its author names sending"},
		{"GET", "https://api.example/x", `{"X-Key": "$secret.k"}`, "", Send, Send, "t", "a keyed GET named sending"},
	} {
		tool := HTTPTool{Name: "t", Method: row.method, URL: row.url, Right: row.named}
		if row.headers != "" {
			_ = json.Unmarshal([]byte(row.headers), &tool.Headers)
		}
		if row.auth != "" {
			tool.Auth = json.RawMessage(row.auth)
		}
		if right, subject := ToolRight(tool); right != row.right || subject != row.subject {
			t.Errorf("%s: ToolRight = %s %q, want %s %q", row.what, right, subject, row.right, row.subject)
		}
	}
}

func TestCovers(t *testing.T) {
	for _, row := range []struct {
		right, has, want string
		covers           bool
		what             string
	}{
		{Network, "api.example", "api.example", true, "a host itself"},
		{Network, "example.com", "api.example.com", true, "a domain its subdomain"},
		{Network, "api.example.com", "example.com", false, "a subdomain not its parent"},
		{Network, "example.com", "badexample.com", false, "a name that ends the same"},
		{Network, "*", "api.example", true, "* any name"},
		{Network, "*", "127.0.0.1", false, "* no address"},
		{Network, "*", "10.0.0.5:8080", false, "* no address with a port"},
		{Network, "127.0.0.1", "127.0.0.1", true, "an address named"},
		{Network, "0.0.1", "127.0.0.1", false, "an address is no domain"},
		{Data, "notes", "notes.items", true, "a module its collection"},
		{Data, "notes", "notes/plan.md", true, "a module its document"},
		{Data, "notes", "notes:models", false, "a module not which models do its work"},
		{Data, "notes", "notesx.items", false, "a module not another that starts the same"},
		{Data, "notes.items", "notes.other", false, "a collection not another"},
		{Data, "*:models", "notes:models", true, "the models of every module"},
		{Data, "*:models", "notes:runs", false, "models not runs"},
		{Data, "*:runs", "notes", false, "runs not data"},
		{Data, "*", "notes/plan.md", true, "everything"},
		{Files, "*", "photos", true, "* any folder"},
		{Files, "*", GPU, false, "* no graphics card"},
		{Files, "photos", "videos", false, "a folder not another"},
		{Modules, "*", "dec.add", true, "* any module"},
		{Modules, "dec", "dec.add", true, "a module its commands"},
		{Modules, "dec.add", "dec.status", false, "a command not another"},
		{Send, "post", "post", true, "a command itself"},
		{Send, "post", "srv.post", false, "a command not a tool of a server named the same"},
		{Send, "*", "post", false, "no * of sending"},
		{Send, "example.com", "shop.example.com", false, "a site not its subdomain"},
		{Money, "openrouter", "openrouter/model", false, "a connection by name only"},
		{Login, "claude-code", "codex", false, "one login not another"},
	} {
		if got := Covers(row.right, row.has, row.want); got != row.covers {
			t.Errorf("%s: Covers(%s, %q, %q) = %v, want %v", row.what, row.right, row.has, row.want, got, row.covers)
		}
	}
}

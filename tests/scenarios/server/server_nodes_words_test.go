package server

import (
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestTheHealthOfANodeReadsInRussian: the row of a node names its connections as they are, local too, and says one
// that serves and one that does not answer in words of one and of many.
func TestTheHealthOfANodeReadsInRussian(t *testing.T) {
	t.Parallel()
	api := nodeModels(t, "from the node")
	// A port below those the system hands out, which no test's server is given: nothing answers there.
	silent := "http://127.0.0.1:1"
	e := harness.New(t)
	e.Node("Mac", api.URL, silent)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": api.URL + "/v1", "local": true, "node": "Mac"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "local", "api": "openai", "url": silent + "/v1", "local": true, "node": "Mac"}, nil)
	ru := func() string {
		var hs []harness.HealthRow
		e.Must("deck.health_list", map[string]any{"lang": "ru"}, &hs)
		for _, h := range hs {
			if h.Subject == "node:Mac" {
				return h.Error
			}
		}
		return ""
	}
	var got string
	e.Until("the row of the node to name its silent connection", func() bool {
		got = ru()
		return strings.Contains(got, `"local"`)
	})
	if !strings.Contains(got, `через него работает подключение "media"`) || !strings.Contains(got, `подключение "local" на его машине не отвечает`) {
		t.Fatalf("the row of the node reads in Russian with its connections as they are: %q", got)
	}
	e.Must("deck.connection_set", map[string]any{"name": "music", "api": "openai", "url": api.URL + "/v2", "local": true, "node": "Mac"}, nil)
	e.Until("the row of the node to name both connections it serves", func() bool {
		got = ru()
		return strings.Contains(got, `через него работают подключения "media", "music"`)
	})
}

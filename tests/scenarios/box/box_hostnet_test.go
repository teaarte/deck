package box

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"
)

// TestABoxOnTheNetworkOfTheMachineResolvesNames: the browser of the shots, on the network of the machine, reads the
// resolver of the machine, as npm needs to fetch Playwright MCP; a box behind its door has none.
func TestABoxOnTheNetworkOfTheMachineResolvesNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the box of macOS is sandbox-exec, which leaves /etc open")
	}
	home := t.TempDir()
	sb := sandbox.New(deckhome.Layout(home))
	for _, hostNet := range []bool{true, false} {
		dir := filepath.Join(home, "box")
		var out bytes.Buffer
		p, err := sb.Run(context.Background(), sandbox.Box{Kind: sandbox.KindBrowser, Mark: sb.Mark(sandbox.KindBrowser, "dns"),
			Argv: []string{"sh", "-c", "test -e /etc/resolv.conf && echo yes || echo no"}, Dir: dir, Home: dir, HostNet: hostNet},
			sandbox.IO{Stdin: strings.NewReader(""), Stdout: &out})
		if err != nil {
			t.Fatal(err)
		}
		_ = p.Wait()
		if got, want := strings.TrimSpace(out.String()), map[bool]string{true: "yes", false: "no"}[hostNet]; got != want {
			t.Fatalf("a box with the network of the machine %v sees its resolver: %s, not %s", hostNet, got, want)
		}
	}
}

package scenarios

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDeckRestartsItselfWithACleanEnvironment(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "deck")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/deck").CombinedOutput(); err != nil {
		t.Fatalf("build deck: %v\n%s", err, out)
	}
	home := t.TempDir()
	cmd := exec.Command(bin, "-addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "DECK_HOME="+home, "DECK_TEST_KEY=sk-marker-7f3a", "NODE_OPTIONS=--require=/tmp/x.js", "LC_ALL=C")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	listening := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "deck listening") {
				listening <- true
			}
		}
	}()
	select {
	case <-listening:
	case <-time.After(30 * time.Second):
		t.Fatal("deck did not start")
	}
	env := startEnv(t, cmd.Process.Pid)
	if !slices.Contains(env, "DECK_HOME="+home) || !slices.Contains(env, "LC_ALL=C") {
		t.Fatalf("deck keeps DECK_HOME and the locale: %q", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "DECK_TEST_KEY=") || strings.HasPrefix(kv, "NODE_OPTIONS=") {
			t.Fatalf("another process of the owner reads %s in the environment of deck", kv)
		}
	}
}

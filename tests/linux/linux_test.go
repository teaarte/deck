package linux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The scenario tests, scripts and their sandbox included, run on Linux with bubblewrap inside a container.
func TestScenariosOnLinux(t *testing.T) {
	if os.Getenv("DECK_LINUX") == "" {
		t.Skip("set DECK_LINUX=1 to run the scenario tests in a Linux container; needs docker")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("docker", "build", "-q", "-t", "deck2-linux-test", filepath.Join(root, "tests", "linux"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	run := exec.Command("docker", "run", "--rm", "--init", "--privileged", "-v", root+":/src:ro", "-v", "deck2-gomod:/go/pkg/mod",
		"-v", "deck2-gocache:/root/.cache/go-build", "-w", "/src", "deck2-linux-test",
		"go", "test", "-count=1", "-v", "./tests/scenarios")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario tests on Linux: %v\n%s", err, out)
	}
	for _, name := range []string{"TestScriptCommandRunsInTheSandbox", "TestScriptKeepsAnIndexInTheProjectDataFolder",
		"TestScriptPackageWorksWithoutPyPIAndMissingProgramShowsInHealth", "TestSchemaMigrationAppliesRecordsWithTheManifestAndRevertsBoth"} {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("%s did not pass on Linux:\n%s", name, out)
		}
	}
}

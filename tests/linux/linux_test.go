package linux

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// linuxTests are the scenario tests that must pass on Linux.
var linuxTests = []string{"TestScriptCommandRunsInTheSandbox", "TestScriptKeepsAnIndexInTheProjectDataFolder",
	"TestScriptInstallsItsPackagesThroughTheProxyAndMissingProgramShowsInHealth", "TestSchemaMigrationAppliesRecordsWithTheManifestAndRevertsBoth",
	"TestUploadedAndRecordFilesReachTheScriptReadOnly", "TestHTTPToolSendsFilesAsItsBody", "TestMCPServerToolsFollowClassesAndFilter",
	"TestACPCommandAgentWorksThroughTheGateway", "TestACPProjectWithoutNetworkHasNoWebThroughTheProxy", "TestACPSelfCheckIsRedWhenTheSandboxOrTheProxyFails",
	"TestTheSandboxClosesTheHomeButForRuntimesAndTheFoldersOfTheProject", "TestAScriptReadsNoFolderOfAnotherProject",
	"TestAScriptSeesNoOtherProcess", "TestAnMCPServerReachesOnlyTheDomainsOfItsProject", "TestTheTunnelOfAStoppedMCPServerCloses",
	"TestMCPCallLostWithItsServerIsAnUnknownOutcome", "TestProjectDeleteStopsTheMCPServersOfTheProject", "TestAFileThatWillNotGoIsInHealthUntilItGoes",
	"TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest", "TestCodexCommandAgentWorksThroughTheGateway"}

// containerTests are the scenario tests that must pass in deck's image: deck as one user, agents and scripts as
// another, under Docker's own confinement with nothing loosened.
var containerTests = []string{"TestInDecksContainerAScriptRunsAsTheUserOfAgents", "TestScriptCommandRunsInTheSandbox",
	"TestUploadedAndRecordFilesReachTheScriptReadOnly", "TestScriptKeepsAnIndexInTheProjectDataFolder", "TestMCPServerToolsFollowClassesAndFilter",
	"TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest"}

// TestLinuxTestsExist fails when a listed test is renamed or gone from tests/scenarios.
func TestLinuxTestsExist(t *testing.T) {
	have := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)
	err := filepath.WalkDir("../scenarios", func(p string, d os.DirEntry, err error) error {
		if err != nil || !strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			have[m[1]] = true
		}
		return err
	})
	if err != nil || len(have) == 0 {
		t.Fatalf("no scenario tests: %v", err)
	}
	for _, name := range append(linuxTests, containerTests...) {
		if !have[name] {
			t.Errorf("%s is listed for Linux but is not a test in tests/scenarios", name)
		}
	}
}

// The scenario tests, scripts and their sandbox included, run on Linux with bubblewrap inside a container, as a user
// without privileges, as deck runs in WSL2; the container unmasks /proc, which a WSL2 machine does not mask.
func TestScenariosOnLinux(t *testing.T) {
	if os.Getenv("DECK_LINUX") == "" {
		t.Skip("set DECK_LINUX=1 to run the scenario tests in a Linux container; needs docker")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	buildImage(t, root)
	run := exec.Command("docker", "run", "--rm", "--init", "--security-opt", "seccomp=unconfined", "--security-opt", "apparmor=unconfined",
		"--security-opt", "systempaths=unconfined", "-v", root+":/src:ro", "-v", "deck2-gomod:/go/pkg/mod", "-v", "deck2-gocache:/cache",
		"-e", "GOCACHE=/cache", "-e", "GOFLAGS=-buildvcs=false", "-w", "/src", "deck2-linux-test", "sh", "-c",
		"chown -R deck /go/pkg/mod /cache && exec setpriv --reuid=deck --regid=deck --init-groups env HOME=/home/deck go test -count=1 -v ./tests/scenarios/...")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario tests on Linux: %v\n%s", err, out)
	}
	for _, name := range linuxTests {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("%s did not pass on Linux:\n%s", name, out)
		}
	}
}

// The scenario tests of deck's image run as root, as deck with its capabilities would, with a user of agents and
// Docker's default seccomp and AppArmor.
func TestScenariosInDecksContainer(t *testing.T) {
	if os.Getenv("DECK_LINUX") == "" {
		t.Skip("set DECK_LINUX=1 to run the scenario tests in a Linux container; needs docker")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	buildImage(t, root)
	run := exec.Command("docker", "run", "--rm", "--init", "-v", root+":/src:ro", "-v", "deck2-gomod:/go/pkg/mod", "-v", "deck2-gocache:/cache",
		"-e", "GOCACHE=/cache", "-e", "GOFLAGS=-buildvcs=false", "-e", "DECK_AGENT_USER=agent", "-w", "/src", "deck2-linux-test",
		"go", "test", "-count=1", "-v", "-run", "^("+strings.Join(containerTests, "|")+")$", "./tests/scenarios/...")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario tests in deck's container: %v\n%s", err, out)
	}
	for _, name := range containerTests {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("%s did not pass in deck's container:\n%s", name, out)
		}
	}
}

// buildImage builds the image of the tests with the version of Codex deck's own image installs.
func buildImage(t *testing.T, root string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG CODEX_VERSION=(\S+)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("deck's Dockerfile names no CODEX_VERSION")
	}
	build := exec.Command("docker", "build", "-q", "--build-arg", "CODEX_VERSION="+string(m[1]), "-t", "deck2-linux-test", filepath.Join(root, "tests", "linux"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
}

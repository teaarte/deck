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

// TestLinuxTestsExist fails when a listed test is renamed or gone from tests/scenarios.
func TestLinuxTestsExist(t *testing.T) {
	files, err := filepath.Glob("../scenarios/*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario tests: %v", err)
	}
	have := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			have[m[1]] = true
		}
	}
	for _, name := range linuxTests {
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
	build := exec.Command("docker", "build", "-q", "-t", "deck2-linux-test", filepath.Join(root, "tests", "linux"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	run := exec.Command("docker", "run", "--rm", "--init", "--security-opt", "seccomp=unconfined", "--security-opt", "apparmor=unconfined",
		"--security-opt", "systempaths=unconfined", "-v", root+":/src:ro", "-v", "deck2-gomod:/go/pkg/mod", "-v", "deck2-gocache:/cache",
		"-e", "GOCACHE=/cache", "-e", "GOFLAGS=-buildvcs=false", "-w", "/src", "deck2-linux-test", "sh", "-c",
		"chown -R deck /go/pkg/mod /cache && exec setpriv --reuid=deck --regid=deck --init-groups env HOME=/home/deck go test -count=1 -v ./tests/scenarios")
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

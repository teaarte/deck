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
	"TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest", "TestCodexCommandAgentWorksThroughTheGateway",
	"TestABoxSeesOnlyWhatItLists", "TestASecretOfAServerIsNotInTheCommandLineNorSeenByAnotherBox",
	"TestTheBoxesACrashedDeckLeftAreKilledByTheirMarks", "TestWithoutABoxDeckStartsNoForeignProgramAndSaysWhatToFix",
	"TestTheBrowserWithoutSignInsReachesNoLocalAddress", "TestTheCLIOfAnAgentReadsNeitherDecksStateNorTheDataOfAnotherProject",
	"TestTheSessionsThatMayGoOnMoveIntoTheHomesOfTheirProjects", "TestTheCodexACrashedDeckLeftIsKilledByTheMarkOfItsBox",
	"TestCodexInItsBoxHasNoSandboxNorWebOfItsOwn", "TestSelfCheckIsRedWhenAutoModeStopsWorking",
	"TestALinkAnAgentPutsInTheSharedFolderOpensNothing", "TestAFolderTurnedIntoALinkLeadsNowhereElse",
	"TestABoxWithEveryHostReachesNoGatewayOfItsHost", "TestAProgramInABoxMakesNoNamespaceOfItsOwn"}

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
	for _, name := range linuxTests {
		if !have[name] {
			t.Errorf("%s is listed for Linux but is not a test in tests/scenarios", name)
		}
	}
}

// The scenario tests, scripts and their sandbox included, run on Linux with bubblewrap inside a container, as a user
// without privileges, as deck runs in its image and in WSL2: the container has the three lines of deck's compose.yaml.
func TestScenariosOnLinux(t *testing.T) {
	if os.Getenv("DECK_LINUX") == "" {
		t.Skip("set DECK_LINUX=1 to run the scenario tests in a Linux container; needs docker")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	buildImage(t, root)
	run := exec.Command("docker", "run", "--rm", "--init", "--security-opt", "seccomp="+filepath.Join(root, "seccomp.json"),
		"--security-opt", "systempaths=unconfined", "--security-opt", "apparmor=deck-box", "-v", root+":/src:ro", "-v", "deck2-gomod:/go/pkg/mod", "-v", "deck2-gocache:/cache",
		"-e", "GOCACHE=/cache", "-e", "GOFLAGS=-buildvcs=false", "-w", "/src", "deck2-linux-test", "sh", "-c",
		"chown -R deck /go/pkg/mod /cache && exec setpriv --reuid=deck --regid=deck --init-groups env HOME=/home/deck go test -count=1 -v ./tests/scenarios/...")
	out, runErr := run.CombinedOutput()
	log := filepath.Join(t.ArtifactDir(), "linux.log")
	if err := os.WriteFile(log, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Errorf("scenario tests on Linux: %v; the whole output is in %s", runErr, log)
	}
	for _, name := range linuxTests {
		if !strings.Contains(string(out), "--- PASS: "+name) {
			t.Errorf("%s did not pass on Linux", name)
		}
	}
	if t.Failed() {
		t.Logf("what failed on Linux:\n%s", failures(string(out)))
	}
}

// failures are the lines of the output of go test -v that tell what failed: each failing test with what it logged
// since its === line, and the packages that failed or panicked.
func failures(out string) string {
	var b strings.Builder
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(trim, "--- FAIL"):
			from := i
			for from > 0 && i-from < 60 && !strings.HasPrefix(strings.TrimSpace(lines[from-1]), "=== ") && !strings.HasPrefix(strings.TrimSpace(lines[from-1]), "--- ") {
				from--
			}
			if from > 0 && strings.HasPrefix(strings.TrimSpace(lines[from-1]), "=== ") {
				from--
			}
			b.WriteString(strings.Join(lines[from:i+1], "\n") + "\n")
		case strings.HasPrefix(trim, "FAIL"), strings.HasPrefix(trim, "panic:"):
			b.WriteString(l + "\n")
		}
	}
	return b.String()
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

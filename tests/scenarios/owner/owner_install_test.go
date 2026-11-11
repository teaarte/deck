package owner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// fakeDocker answers install.sh as a docker whose image ghcr.io/teaarte/deck:latest is of release v0.3.0; fakeCurl
// writes each file it is asked for and logs its address.
const (
	fakeDocker = `#!/bin/sh
echo "docker $*" >> "$LOG"
case "$*" in
"image inspect"*) echo v0.3.0 ;;
*"deck pair"*) echo "http://deck.localhost:7700/#pair=x" ;;
esac
exit 0
`
	fakeCurl = `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case $1 in -o) out=$2; shift ;; -*) ;; *) url=$1 ;; esac
  shift
done
echo "curl $url" >> "$LOG"
[ -z "$out" ] || echo "# $url" > "$out"
`
)

// TestTheInstallerTakesTheFilesOfTheReleaseOfItsImage: run as the README says, install.sh downloads compose.yaml,
// seccomp.json and the AppArmor profile of the release the image it pulls is, not those of master.
func TestTheInstallerTakesTheFilesOfTheReleaseOfItsImage(t *testing.T) {
	t.Parallel()
	bin, dir := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "log")
	for name, body := range map[string]string{"docker": fakeDocker, "curl": fakeCurl} {
		if err := harness.WriteProgram(filepath.Join(bin, name), []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.Open(filepath.Join(harness.Root, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	// curl … | sh: the script comes on stdin, from no checkout.
	cmd := exec.Command("sh")
	cmd.Stdin, cmd.Dir = script, t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "LOG="+log, "DECK_DIR="+dir, "HOME="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	said, _ := os.ReadFile(log)
	for _, f := range []string{"compose.yaml", "seccomp.json", "deck-box.apparmor"} {
		if !strings.Contains(string(said), "curl https://raw.githubusercontent.com/teaarte/deck/v0.3.0/"+f+"\n") {
			t.Fatalf("install.sh takes %s of the release of its image, v0.3.0:\n%s", f, said)
		}
	}
	if strings.Contains(string(said), "/master/") {
		t.Fatalf("install.sh takes nothing of master:\n%s", said)
	}
}

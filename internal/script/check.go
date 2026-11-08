package script

import (
	"fmt"
	"runtime"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

var interpreters = map[string]string{"python": "python3", "node": "node", "bash": "bash"}

// Check reports the scripts of a manifest whose programs are not on this machine, by script name.
func (x *Exec) Check(m *manifest.Manifest) map[string]error {
	problems := map[string]error{}
	for _, s := range m.Scripts {
		if err := x.check(s); err != nil {
			problems[s.Name] = err
		}
	}
	return problems
}

func (x *Exec) check(s manifest.Script) error {
	progs := s.Programs
	if p := interpreters[s.Runtime]; p != "" {
		progs = append([]manifest.Program{{Name: p}}, progs...)
	}
	for _, prog := range progs {
		if prog.Name == "python3" && Runtimes().Python != "" {
			continue
		}
		if _, err := sandbox.Find(prog.Name); err != nil {
			if x.Image != "" {
				return fault.New(fault.ProgramMissing, fmt.Sprintf("program %q that script %s needs is not in the image of deck %s; the owner updates the image", prog.Name, s.Name, x.Image))
			}
			return fault.New(fault.ProgramMissing, fmt.Sprintf("program %q that script %s needs is not on this machine; the owner installs it: %s", prog.Name, s.Name, install(prog)))
		}
	}
	return nil
}

// install is how the owner installs a program on this machine: the way the module names when it is of this system,
// else the package manager of the system.
func install(p manifest.Program) string {
	brew := strings.HasPrefix(p.Install, "brew ")
	if runtime.GOOS == "darwin" {
		if p.Install != "" && (brew || !strings.Contains(p.Install, "apt")) {
			return p.Install
		}
		return "brew install " + p.Name
	}
	if p.Install != "" && !brew {
		return p.Install
	}
	if sandbox.WSL() {
		return "wsl -d deck -u root -- apt-get install -y " + p.Name
	}
	return "sudo apt-get install " + p.Name
}

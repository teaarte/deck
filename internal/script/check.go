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
				return fault.New(fault.ProgramMissing, fmt.Sprintf("program %q that script %s needs is not in deck's image; the owner updates the image of deck", prog.Name, s.Name))
			}
			if how := install(prog.Install); how != "" {
				return fault.New(fault.ProgramMissing, fmt.Sprintf("program %q that script %s needs is not on this machine; the owner installs it: %s", prog.Name, s.Name, how))
			}
			return fault.New(fault.ProgramMissing, fmt.Sprintf("program %q that script %s needs is not on this machine", prog.Name, s.Name))
		}
	}
	return nil
}

// install is how the owner installs a program on this machine by the text of its module: a plain brew install of a
// formula becomes apt-get on Linux, a command of apt says nothing on a Mac, any other text stays.
func install(text string) string {
	f := strings.Fields(text)
	if len(f) > 1 && f[0] == "sudo" {
		f = f[1:]
	}
	switch {
	case len(f) == 0:
		return ""
	case runtime.GOOS == "darwin" && (f[0] == "apt" || f[0] == "apt-get"):
		return ""
	case runtime.GOOS != "darwin" && len(f) == 3 && f[0] == "brew" && f[1] == "install" && !strings.HasPrefix(f[2], "-"):
		if sandbox.WSL() {
			return "wsl -d deck -u root -- apt-get install -y " + f[2]
		}
		return "sudo apt-get install " + f[2]
	}
	return text
}

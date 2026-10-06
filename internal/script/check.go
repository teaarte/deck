package script

import (
	"fmt"
	"os/exec"

	"deck2/internal/manifest"
)

var interpreters = map[string]string{"python": "python3", "node": "node", "bash": "bash"}

// Check reports the scripts of a manifest whose programs are not on this machine, by script name.
func Check(m *manifest.Manifest) map[string]error {
	problems := map[string]error{}
	for _, s := range m.Scripts {
		if err := check(s); err != nil {
			problems[s.Name] = err
		}
	}
	return problems
}

func check(s manifest.Script) error {
	progs := s.Programs
	if p := interpreters[s.Runtime]; p != "" {
		progs = append([]manifest.Program{{Name: p}}, progs...)
	}
	for _, prog := range progs {
		if _, err := exec.LookPath(prog.Name); err != nil {
			if prog.Install != "" {
				return fmt.Errorf("program %q that script %s needs is not on this machine; the owner installs it: %s", prog.Name, s.Name, prog.Install)
			}
			return fmt.Errorf("program %q that script %s needs is not on this machine", prog.Name, s.Name)
		}
	}
	return nil
}

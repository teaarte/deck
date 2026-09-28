package scenarios

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const realPythonScript = `#!/usr/bin/env -S uv run --script
# /// script
# dependencies = ["six==1.16.0"]
# ///
import json, six
print(json.dumps({"value": six.__version__}))
`

const realNodeScript = `const { execFileSync } = require("node:child_process");
const { createRequire } = require("node:module");
const dir = process.env.DECK_DATA + "/node";
execFileSync("npm", ["install", "--prefix", dir, "--no-audit", "--no-fund", "left-pad@1.3.0"], { stdio: ["ignore", 2, 2] });
const leftPad = createRequire(dir + "/")("left-pad");
console.log(JSON.stringify({ value: leftPad("7", 3, "0") }));
`

// TestRealScriptPackages installs packages from PyPI and npm through the proxy of deck with the registries in domains.read.
func TestRealScriptPackages(t *testing.T) {
	if os.Getenv("DECK_REAL_PACKAGES") == "" {
		t.Skip("set DECK_REAL_PACKAGES=1 to install from PyPI and npm")
	}
	for _, prog := range []string{"uv", "npm"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Skipf("%s is not installed", prog)
		}
	}
	e := newEnv(t, nil)
	cases := []struct{ project, runtime, file, src, domains, want string }{
		{"py", `"runtime": "exec", "programs": ["uv"]`, "scripts/main.py", realPythonScript, `"pypi.org", "files.pythonhosted.org"`, "1.16.0"},
		{"js", `"runtime": "node", "programs": ["npm"]`, "scripts/main.js", realNodeScript, `"registry.npmjs.org"`, "007"},
	}
	for _, c := range cases {
		m := strings.NewReplacer(`"scripts/main.py", "runtime": "python"`, `"`+c.file+`", `+c.runtime).
			Replace(scriptManifest("use", "", `, "domains": {"read": [`+c.domains+`]}`))
		e.createProject(c.project, m)
		e.saveScript(c.project, c.file, c.src)
		p := e.waitScript(e.start(c.project, "use", nil), "done", "error")
		if p.State != "done" || !strings.Contains(string(p.Result), c.want) {
			t.Fatalf("%s: the script installs its package from the registry: %+v", c.project, p)
		}
	}
}

package real

import (
	"os/exec"
	"strings"
	"testing"

	"deck2/tests/harness"
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
	harness.Needs(t, "DECK_REAL_PACKAGES")
	for _, prog := range []string{"uv", "npm"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Skipf("%s is not installed", prog)
		}
	}
	e := harness.New(t)
	cases := []struct{ project, runtime, file, src, domains, want string }{
		{"py", `"runtime": "exec", "programs": ["uv"]`, "scripts/main.py", realPythonScript, `"pypi.org", "files.pythonhosted.org"`, "1.16.0"},
		{"js", `"runtime": "node", "programs": ["npm"]`, "scripts/main.js", realNodeScript, `"registry.npmjs.org"`, "007"},
	}
	for _, c := range cases {
		m := strings.NewReplacer(`"scripts/main.py", "runtime": "python"`, `"`+c.file+`", `+c.runtime).
			Replace(harness.ScriptManifest("use", "", `, "domains": {"read": [`+c.domains+`]}`))
		e.Project(c.project, m)
		e.SaveScript(c.project, c.file, c.src)
		p := e.WaitState(e.Start(c.project, "use", nil), "done", "error")
		if p.State != "done" || !strings.Contains(string(p.Result), c.want) {
			t.Fatalf("%s: the script installs its package from the registry: %+v", c.project, p)
		}
	}
}

const realHFScript = `#!/usr/bin/env -S uv run --script
# /// script
# dependencies = ["huggingface_hub"]
# ///
import json, os
from huggingface_hub import hf_hub_download
path = hf_hub_download("hf-internal-testing/tiny-random-LlamaForCausalLM", "model.safetensors")
print(json.dumps({"size": os.path.getsize(path), "cached": path.startswith(os.path.join(os.environ["DECK_DATA"], ".cache"))}))
`

const realGitScript = `import json, os, subprocess
d = os.path.join(os.environ["DECK_DATA"], "hello")
subprocess.run(["git", "clone", "-q", "--depth", "1", "https://github.com/octocat/Hello-World.git", d], check=True)
subprocess.run(["git", "-C", d, "-c", "user.name=deck", "-c", "user.email=deck@localhost", "commit", "-q", "--allow-empty", "-m", "x"], check=True)
n = subprocess.run(["git", "-C", d, "rev-list", "--count", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
print(json.dumps({"commits": int(n)}))
`

const realFFmpegScript = `import json, os, subprocess
out = os.path.join(os.environ["DECK_RESULTS"], "test.mp4")
subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=d=1:s=320x240", "-pix_fmt", "yuv420p", out], check=True)
d = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out], check=True, capture_output=True, text=True).stdout
print(json.dumps({"seconds": round(float(d))}))
`

// realRemotionScript installs Remotion, bundles a composition and runs Chrome Headless Shell, fetched with curl
// as the owner's module does; the render itself needs a local port, which scripts do not have yet.
const realRemotionScript = `const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
(async () => {
  const dir = path.join(process.env.DECK_DATA, "rem");
  fs.mkdirSync(path.join(dir, "src"), { recursive: true });
  fs.writeFileSync(path.join(dir, "package.json"), JSON.stringify({ private: true }));
  execFileSync("npm", ["install", "--prefix", dir, "--no-audit", "--no-fund", "remotion@4.0.532", "@remotion/bundler@4.0.532",
    "@remotion/renderer@4.0.532", "react@19.1.0", "react-dom@19.1.0"], { stdio: ["ignore", 2, 2] });
  fs.writeFileSync(path.join(dir, "src", "index.jsx"), [
    'import React from "react";',
    'import { Composition, registerRoot, AbsoluteFill } from "remotion";',
    'const Hello = () => <AbsoluteFill style={{ background: "white" }}>hello</AbsoluteFill>;',
    'registerRoot(() => <Composition id="hello" component={Hello} durationInFrames={10} fps={10} width={64} height={64} />);'].join("\n"));
  const { bundle } = require(path.join(dir, "node_modules", "@remotion", "bundler"));
  const site = await bundle({ entryPoint: path.join(dir, "src", "index.jsx"), outDir: path.join(process.env.TMPDIR, "site") });
  const mac = process.platform === "darwin";
  const name = mac ? "chrome-headless-shell-mac-arm64" : "chrome-headless-shell-linux64";
  const url = "https://storage.googleapis.com/chrome-for-testing-public/149.0.7790.0/" + (mac ? "mac-arm64" : "linux64") + "/" + name + ".zip";
  const zip = path.join(process.env.TMPDIR, "chrome.zip");
  execFileSync("curl", ["-sSfL", "-o", zip, url], { stdio: ["ignore", 2, 2] });
  execFileSync("unzip", ["-q", "-o", zip, "-d", path.join(dir, "chrome")], { stdio: ["ignore", 2, 2] });
  const version = execFileSync(path.join(dir, "chrome", name, "chrome-headless-shell"), ["--version"]).toString().trim();
  console.log(JSON.stringify({ bundled: fs.existsSync(path.join(site, "index.html")), chrome: version }));
})().catch((e) => { console.error(e); process.exit(1); });
`

// TestRealReferenceScripts runs what modules do in the sandbox of scripts: packages from PyPI and npm, a model of
// Hugging Face, git over https, ffmpeg, Remotion with Chrome, and an MCP server run by npx.
func TestRealReferenceScripts(t *testing.T) {
	harness.Needs(t, "DECK_REAL_PACKAGES")
	for _, prog := range []string{"uv", "npm", "npx", "git", "ffmpeg", "curl", "unzip"} {
		if _, err := exec.LookPath(prog); err != nil {
			t.Skipf("%s is not installed", prog)
		}
	}
	e := harness.New(t)
	cases := []struct{ project, runtime, file, src, domains, want string }{
		{"hf", `"runtime": "exec", "programs": ["uv"]`, "scripts/main.py", realHFScript, `"pypi.org", "files.pythonhosted.org", "huggingface.co", "hf.co"`, `"cached":true`},
		{"git", `"runtime": "python", "programs": ["git"]`, "scripts/main.py", realGitScript, `"github.com"`, `"commits":2`},
		{"ffmpeg", `"runtime": "python", "programs": ["ffmpeg", "ffprobe"]`, "scripts/main.py", realFFmpegScript, `"pypi.org"`, `"seconds":1`},
		{"remotion", `"runtime": "node", "programs": ["npm", "curl", "unzip"]`, "scripts/main.js", realRemotionScript, `"registry.npmjs.org", "storage.googleapis.com"`, `"bundled":true`},
	}
	for _, c := range cases {
		t.Run(c.project, func(t *testing.T) {
			m := strings.NewReplacer(`"scripts/main.py", "runtime": "python"`, `"`+c.file+`", `+c.runtime).
				Replace(harness.ScriptManifest("use", "", `, "domains": {"read": [`+c.domains+`]}`))
			e.Project(c.project, m)
			e.SaveScript(c.project, c.file, c.src)
			id := e.Start(c.project, "use", nil)
			if p := e.WaitState(id, "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), c.want) {
				t.Fatalf("the script %s runs in the sandbox: %+v\n%v", c.project, p, e.Log(id))
			}
		})
	}
	t.Run("npx", func(t *testing.T) {
		e.Project("everything", harness.Manifest(`{"commands": [{"name": "echo", "title": "Echo", "description": "Echoes a message.", "class": "internal",
    "input": {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]},
    "executor": {"type": "tool", "tool": "everything.echo"}}],
  "tools": {"mcp": {"servers": {"everything": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-everything"],
    "toolFilter": ["echo"], "classes": {"echo": "internal"}}}}}}`))
		id := e.Start("everything", "echo", map[string]any{"message": "sandboxed hello"})
		if p := e.WaitState(id, "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), "sandboxed hello") {
			t.Fatalf("an MCP server run by npx answers in the sandbox: %+v", p)
		}
	})
}

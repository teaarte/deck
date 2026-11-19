package modules

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// heavyModule is a module of one script of runtime with the rights given.
func heavyModule(runtime, rights string) string {
	file := "scripts/main.py"
	if runtime == "node" {
		file = "scripts/main.mjs"
	}
	return harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "make", "title": "Make", "description": "Makes a picture.",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}}],
  "scripts": [{"name": "main", "file": %q, "runtime": %q}]%s}`, file, runtime, rights))
}

const gpuRight = `, "rights": [{"right": "files", "what": ["gpu"], "why": "renders video"}]`

// header is a script with a PEP 723 header of the packages given.
func header(deps ...string) string {
	quoted := make([]string, len(deps))
	for i, d := range deps {
		quoted[i] = fmt.Sprintf("%q", d)
	}
	return "# /// script\n# requires-python = \">=3.11\"\n# dependencies = [\n#   " + strings.Join(quoted, ",\n#   ") + ",\n# ]\n# ///\nimport json\nprint(json.dumps({}))\n"
}

// TestAModuleThatKeepsAModelInDeckIsWarnedAndSaved: gpu with torch, mlx or CUDA in a header, Hugging Face in a script or
// its network, a script on node — warned on a read, a check and a save, and saved; gpu for rendering alone is not.
func TestAModuleThatKeepsAModelInDeckIsWarnedAndSaved(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for i, c := range []struct {
		name, runtime, rights, script, warn string
	}{
		{"gpu with torch", "python", gpuRight, header("torch>=2.3", "pillow"), "files gpu with torch in the PEP 723 header of script main"},
		{"mlx without gpu", "python", "", header("mlx-audio[tts]==0.2"), "mlx-audio[tts]==0.2 in its PEP 723 header runs a model on a graphics card"},
		{"cuda torch", "python", "", header("torch-cuda"), "torch-cuda in its PEP 723 header runs a model on a graphics card"},
		{"a download from Hugging Face", "python", "", header("requests") + `import urllib.request
urllib.request.urlretrieve("https://huggingface.co/org/model/resolve/main/model.safetensors", "m")
`, "script main downloads from Hugging Face"},
		{"Hugging Face in its network", "python", `, "rights": [{"right": "network", "what": ["huggingface.co"], "why": "models"}]`, header(), "network huggingface.co: its programs download from Hugging Face"},
		{"node", "node", "", "console.log('{}')\n", "script main runs on node: a script of a module is Python"},
		{"CUDA in the extras", "python", "", header("jax[cuda12]>=0.4"), "jax[cuda12]>=0.4 in its PEP 723 header runs a model on a graphics card"},
		{"gpu for rendering", "python", gpuRight, header("pillow", "imageio-ffmpeg", "mlxtend") + "# a comment of torch [1]\n", ""},
		{"torch without gpu", "python", "", header("torch"), ""},
		{"torch commented out", "python", gpuRight, "# /// script\n# dependencies = [\n#   # \"torch\",\n#   \"pillow\",  # see [1]\n#   \"diffusers\",\n# ]\n# ///\n", "files gpu with diffusers"},
		{"the cloud of Hugging Face", "python", `, "rights": [{"right": "network", "what": ["router.huggingface.co"], "why": "inference"}]`, header("huggingface_hub") + `from huggingface_hub import InferenceClient
InferenceClient(base_url="https://router.huggingface.co/v1")
`, ""},
	} {
		name := fmt.Sprintf("heavy%d", i)
		weights := func(ws []string) []string {
			return slices.DeleteFunc(slices.Clone(ws), func(w string) bool {
				return !strings.Contains(w, "weights of models do not live in deck") && !strings.Contains(w, "runs on node")
			})
		}
		e.Project(name, heavyModule(c.runtime, c.rights))
		file := "scripts/main.py"
		if c.runtime == "node" {
			file = "scripts/main.mjs"
		}
		e.SaveScript(name, file, c.script)
		var check struct {
			Ok       bool
			Refused  []string
			Warnings []string
		}
		e.Must("deck.manifest_check", map[string]any{"project": name}, &check)
		got := weights(check.Warnings)
		if !check.Ok || c.warn == "" && len(got) > 0 || c.warn != "" && (len(got) != 1 || !strings.Contains(got[0], c.warn)) {
			t.Fatalf("%s: manifest_check takes it and warns %q: %+v", c.name, c.warn, check)
		}
		var read struct{ Warnings []string }
		e.Must("deck.manifest_get", map[string]any{"project": name}, &read)
		if w := weights(read.Warnings); c.warn != "" && (len(w) != 1 || !strings.Contains(w[0], c.warn)) || c.warn == "" && len(w) > 0 {
			t.Fatalf("%s: a read of the module warns %q: %v", c.name, c.warn, read.Warnings)
		}
		var saved struct {
			Revision int64
			Warnings []string
		}
		e.Must("deck.manifest_save", map[string]any{"project": name, "old": []string{`"Makes a picture."`}, "new": []string{`"Makes a picture again."`}}, &saved)
		if w := weights(saved.Warnings); saved.Revision < 2 || c.warn != "" && len(w) != 1 || c.warn == "" && len(w) > 0 {
			t.Fatalf("%s: a save takes it and warns %q: %+v", c.name, c.warn, saved)
		}
	}
}

// A script of the form before 2026-11-14, exec with uv run in its shebang, is warned to take runtime python; the save
// still takes it, and runtime python is not warned.
func TestAnExecScriptOfUvIsWarnedToRunAsPython(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for name, c := range map[string]struct{ runtime, shebang, warn string }{
		"uvexec":  {"exec", "#!/usr/bin/env -S uv run --script", "script main runs by exec with uv run in its shebang: give it runtime python"},
		"uvpy312": {"exec", "#!/usr/bin/env -S  uv run --python 3.12 --script", "script main runs by exec with uv run in its shebang"},
		"uvpy":    {"python", "#!/usr/bin/env -S uv run --script", ""},
		"python3": {"exec", "#!/usr/bin/env python3", ""},
	} {
		e.Project(name, heavyModule(c.runtime, ""))
		e.SaveScript(name, "scripts/main.py", c.shebang+"\n"+header("requests"))
		var check struct {
			Ok       bool
			Warnings []string
		}
		e.Must("deck.manifest_check", map[string]any{"project": name}, &check)
		warned := slices.ContainsFunc(check.Warnings, func(w string) bool { return strings.Contains(w, "uv run in its shebang") })
		if !check.Ok || warned != (c.warn != "") || c.warn != "" && !slices.ContainsFunc(check.Warnings, func(w string) bool { return strings.Contains(w, c.warn) }) {
			t.Fatalf("%s: manifest_check takes it and warns %q: %+v", name, c.warn, check)
		}
	}
}

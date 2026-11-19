package manifest

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// model is what the warnings of Heavy tell a module to do instead of keeping a model in deck.
const model = "weights of models do not live in deck: a model is a connection, a ready open-source server (Ollama, vLLM, " +
	"llama.cpp, whisper.cpp, LocalAI) on the owner's machine or a node, or a cloud API; a small model a package computes " +
	"on the CPU stays a package"

// onCard are the packages of a PEP 723 header that run a model on a graphics card; a name of mlx- counts too.
var onCard = []string{"mlx", "onnxruntime-gpu", "tensorrt", "vllm"}

// learning are the packages of a PEP 723 header that load the weights of a model.
var learning = []string{"torch", "torchvision", "torchaudio", "diffusers", "transformers", "accelerate", "tensorflow",
	"jax", "onnxruntime", "onnxruntime-gpu", "mlx", "sentence-transformers"}

// fromHF is a script that downloads files of a model from Hugging Face; its inference API is a cloud API like others.
var fromHF = regexp.MustCompile(`\b(?:huggingface\.co|hf\.co)/[\w.-]+/[\w.-]+/(?:resolve|blob)/|` +
	`\bgit(?:\s+lfs)?\s+clone\s+\S*\b(?:huggingface\.co|hf\.co)/|\b(?:snapshot_download|hf_hub_download|from_pretrained)\(`)

// uvShebang is a first line that runs a script by uv, as a Python script ran before runtime python.
var uvShebang = regexp.MustCompile(`^\x{FEFF}?#!\S*env\s+-S\s+uv\b[^\n]*\brun\b`)

// tomlString is a string of a line of TOML.
var tomlString = regexp.MustCompile(`"([^"]+)"|'([^']+)'`)

// Heavy are the warnings of a module whose scripts look like they keep a model in deck — gpu with a package that loads
// weights, a package of a graphics card, a download from Hugging Face — or do not run as Python; src is a file's text.
func (m *Manifest) Heavy(src func(file string) string) []string {
	var out []string
	gpu := slices.ContainsFunc(m.Rights, func(d Declared) bool { return d.Right == Files && slices.Contains(d.What, GPU) })
	for _, d := range m.Rights {
		if d.Right != Network || d.Whom() != Programs {
			continue
		}
		for _, s := range d.What {
			if host, _, _ := strings.Cut(strings.ToLower(s), ":"); hubHost(host) {
				out = append(out, fmt.Sprintf("network %s: its programs download from Hugging Face; %s", s, model))
			}
		}
	}
	for _, sc := range m.Scripts {
		if sc.Runtime == "node" {
			out = append(out, fmt.Sprintf("script %s runs on node: a script of a module is Python with a PEP 723 header; node only starts a ready JS engine, such as Remotion", sc.Name))
		}
		text := src(sc.File)
		if sc.Runtime == "exec" && uvShebang.MatchString(text) {
			out = append(out, fmt.Sprintf("script %s runs by exec with uv run in its shebang: give it runtime python, which runs it by uv run --script on deck's python", sc.Name))
		}
		deps := requirements(text)
		if i := slices.IndexFunc(deps, onGPU); i >= 0 {
			out = append(out, fmt.Sprintf("script %s: %s in its PEP 723 header runs a model on a graphics card; %s", sc.Name, deps[i], model))
		} else if i := slices.IndexFunc(deps, func(r string) bool { return slices.Contains(learning, packageName(r)) }); gpu && i >= 0 {
			out = append(out, fmt.Sprintf("files gpu with %s in the PEP 723 header of script %s: gpu is for rendering and video (Chrome, ffmpeg), not for models; %s", packageName(deps[i]), sc.Name, model))
		}
		if fromHF.MatchString(text) {
			out = append(out, fmt.Sprintf("script %s downloads from Hugging Face; %s", sc.Name, model))
		}
	}
	return out
}

// hubHost says a host serves the files of models of Hugging Face, not its inference API.
func hubHost(host string) bool {
	if strings.HasPrefix(host, "router.") || strings.HasPrefix(host, "api-inference.") {
		return false
	}
	return host == "huggingface.co" || host == "hf.co" || strings.HasSuffix(host, ".huggingface.co") || strings.HasSuffix(host, ".hf.co")
}

// onGPU says a requirement runs a model on a graphics card: a package of one, or CUDA in its name or extras.
func onGPU(r string) bool {
	name := packageName(r)
	return slices.Contains(onCard, name) || strings.HasPrefix(name, "mlx-") || strings.HasPrefix(name, "nvidia-") ||
		strings.Contains(r, "cuda")
}

// packageName is the name of a requirement of PEP 508, lower case with - for _.
func packageName(r string) string {
	if i := strings.IndexAny(r, " <>=!~;[@("); i >= 0 {
		r = r[:i]
	}
	return strings.ReplaceAll(r, "_", "-")
}

// requirements are the dependencies of the PEP 723 header of a Python script, lower case; none without one.
func requirements(src string) []string {
	var out []string
	in, list := false, false
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "# /// script":
			in = true
			continue
		case !in:
			continue
		case t == "# ///":
			return out
		}
		t = uncommented(strings.TrimSpace(strings.TrimPrefix(t, "#")))
		if strings.HasPrefix(t, "dependencies") {
			list = true
			if _, after, ok := strings.Cut(t, "="); ok {
				t = after
			}
		}
		if !list {
			continue
		}
		for _, q := range tomlString.FindAllStringSubmatch(t, -1) {
			out = append(out, strings.ToLower(strings.TrimSpace(q[1]+q[2])))
		}
		if strings.Contains(tomlString.ReplaceAllString(t, ""), "]") {
			list = false
		}
	}
	return nil
}

// uncommented is a line of TOML without its comment, a # outside a string.
func uncommented(line string) string {
	var quote rune
	for i, c := range line {
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			return line[:i]
		}
	}
	return line
}

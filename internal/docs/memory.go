package docs

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

const (
	// MemoryDir holds the memory of a project: the index and one document per fact.
	MemoryDir   = "memory/"
	MemoryIndex = MemoryDir + "MEMORY.md"
	// IndexLimit is how many lines of an index reach a prompt, each of lineLimit bytes at most.
	IndexLimit = 150
	lineLimit  = 400
	indexBytes = 32000
)

var memoryTypes = []string{"user", "feedback", "project", "reference"}

// checkMemoryText checks that memory documents sit in memory/ and a fact carries its frontmatter.
func checkMemoryText(kind, name, text string) error {
	if (kind == KindMemory) != strings.HasPrefix(name, MemoryDir) {
		return registry.Invalid("memory documents are named memory/<name>.md, and only memory documents are")
	}
	if kind != KindMemory || name == MemoryIndex {
		return nil
	}
	file := strings.TrimPrefix(name, MemoryDir)
	slug, ok := strings.CutSuffix(file, ".md")
	if !ok || strings.Contains(slug, "/") {
		return registry.Invalid("a memory fact is one file memory/<name>.md")
	}
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return registry.Invalid("a memory fact starts with YAML frontmatter between --- lines: name, description, type")
	}
	var meta struct {
		Name, Description, Type string
		Expires                 string
		Metadata                struct{ Type string }
	}
	if err := yaml.Unmarshal([]byte(front[:end]), &meta); err != nil {
		return registry.Invalid("the frontmatter of a memory fact is not YAML: %v", err)
	}
	if meta.Type == "" {
		meta.Type = meta.Metadata.Type
	}
	switch {
	case meta.Name != slug:
		return registry.Invalid("a memory fact needs \"name: %s\", the name of its file", slug)
	case strings.TrimSpace(meta.Description) == "":
		return registry.Invalid("a memory fact needs \"description\": one line that says when it matters")
	case !slices.Contains(memoryTypes, meta.Type):
		return registry.Invalid("a memory fact needs \"type:\" in its frontmatter (or under metadata:), one of %s; it has %q",
			strings.Join(memoryTypes, ", "), meta.Type)
	}
	if meta.Expires != "" {
		if _, err := time.Parse(time.DateOnly, meta.Expires); err != nil {
			return registry.Invalid("expires of a memory fact is a date, YYYY-MM-DD")
		}
	}
	return nil
}

// IndexLines counts the lines of an index.
func IndexLines(text string) int {
	return len(strings.Split(strings.TrimRight(text, "\n"), "\n"))
}

// CutIndex is the start of an index: at most IndexLimit lines of at most lineLimit bytes and indexBytes in all;
// cut tells what was left out, outside the data.
func CutIndex(text string) (start, cut string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var kept []string
	size, short := 0, false
	for _, l := range lines {
		if len(kept) == IndexLimit || size+len(l) > indexBytes {
			break
		}
		if len(l) > lineLimit {
			l, short = untrusted.Cut(l, lineLimit), true
		}
		kept = append(kept, l)
		size += len(l) + 1
	}
	switch {
	case len(kept) < len(lines):
		cut = "deck cut " + strconv.Itoa(len(lines)-len(kept)) + " more lines of this index."
	case short:
		cut = "deck shortened lines of this index longer than " + strconv.Itoa(lineLimit) + " bytes."
	}
	return strings.Join(kept, "\n"), cut
}

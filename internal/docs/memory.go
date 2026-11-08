package docs

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/schemas"
)

const (
	// MemoryDir holds the memory of a project: the index and one document per fact.
	MemoryDir   = "memory/"
	MemoryIndex = MemoryDir + "MEMORY.md"
	// IndexLimit is how many lines of an index reach a prompt, each of lineLimit bytes at most.
	IndexLimit = 150
	// LessonLimit is how many lines of the lessons section of an index, under LessonsHeading, reach a prompt.
	LessonLimit    = 40
	LessonsHeading = "## Lessons"
	lineLimit      = 400
	indexBytes     = 32000
	// MemoryBudget is how many bytes of memory a turn loads in all: the indexes and the lessons of its command; each
	// part present gets at least MemoryFloor of it.
	MemoryBudget = 12 << 10
	MemoryFloor  = MemoryBudget / 4
)

var memoryTypes = []string{"user", "feedback", "project", "reference", "lesson"}

// Lesson is the type of a fact deck's agents concluded themselves, not the owner's words.
const Lesson = "lesson"

// sourceRef is one reference of the source of a lesson: a process, a topic or an incident of a process.
var sourceRef = regexp.MustCompile(`^(process:[0-9]+|topic:[0-9]+|incident:[0-9]+\.[a-z_]+)$`)

// Fact is the frontmatter of a memory fact.
type Fact struct {
	Name, Description, Type string
	Source, Command         string
	Expires                 string
	// Confirmed is how many times the situation of a lesson came again, LastConfirmed the day it last did.
	Confirmed     string
	LastConfirmed string `yaml:"last_confirmed"`
	Metadata      struct{ Type string }
}

// Times is how many times a lesson was confirmed, 1 when it does not say.
func (f Fact) Times() int {
	if n, err := strconv.Atoi(f.Confirmed); err == nil && n > 0 {
		return n
	}
	return 1
}

// ParseFact reads the frontmatter of a memory fact; the type may sit under metadata.
func ParseFact(text string) (Fact, error) {
	var f Fact
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return f, registry.Invalid("a memory fact starts with YAML frontmatter between --- lines: name, description, type")
	}
	if err := yaml.Unmarshal([]byte(front[:end]), &f); err != nil {
		return f, registry.Invalid("the frontmatter of a memory fact is not YAML: %v", err)
	}
	if f.Type == "" {
		f.Type = f.Metadata.Type
	}
	return f, nil
}

// Expired says the fact stopped being true before the day of now.
func (f Fact) Expired(now time.Time) bool {
	return f.Expires != "" && f.Expires < now.Format(time.DateOnly)
}

// CheckMemory checks that memory documents sit in memory/ and a fact carries its frontmatter.
func CheckMemory(kind, name, text string) error {
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
	meta, err := ParseFact(text)
	if err != nil {
		return err
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
	if n, err := strconv.Atoi(meta.Confirmed); meta.Confirmed != "" && (err != nil || n < 1) {
		return registry.Invalid("confirmed of a memory fact is how many times it held, a whole number from 1")
	}
	if meta.LastConfirmed != "" {
		if _, err := time.Parse(time.DateOnly, meta.LastConfirmed); err != nil {
			return registry.Invalid("last_confirmed of a memory fact is a date, YYYY-MM-DD")
		}
	}
	if meta.Type != Lesson {
		return nil
	}
	if strings.TrimSpace(meta.Source) == "" {
		return registry.Invalid("a lesson needs \"source\": what taught it, process:<id>, topic:<id> or incident:<process id>.<kind>, comma-separated")
	}
	for _, ref := range strings.Split(meta.Source, ",") {
		if !sourceRef.MatchString(strings.TrimSpace(ref)) {
			return registry.Invalid("source %q of a lesson is not process:<id>, topic:<id> or incident:<process id>.<kind>", strings.TrimSpace(ref))
		}
	}
	if meta.Command != "" && !schemas.Name.MatchString(meta.Command) {
		return registry.Invalid("command %q of a lesson is not the name of a command", meta.Command)
	}
	return nil
}

// IndexLines counts the lines of an index.
func IndexLines(text string) int {
	return len(strings.Split(strings.TrimRight(text, "\n"), "\n"))
}

// LessonLines counts the lines of the lessons section of an index, blank ones aside.
func LessonLines(text string) int {
	_, n := lessonsCut(strings.Split(strings.TrimRight(text, "\n"), "\n"))
	return n
}

// isLessons says a line is the heading of the lessons section of an index.
func isLessons(line string) bool { return strings.EqualFold(strings.TrimSpace(line), LessonsHeading) }

// lessonsCut keeps at most LessonLimit lines of the lessons section; n counts the lines of the section.
func lessonsCut(lines []string) (kept []string, n int) {
	in := false
	for _, l := range lines {
		switch {
		case isLessons(l):
			in = true
		case strings.HasPrefix(l, "## "):
			in = false
		case in && strings.TrimSpace(l) != "":
			if n++; n > LessonLimit {
				continue
			}
		case in && n >= LessonLimit:
			continue
		}
		kept = append(kept, l)
	}
	return kept, n
}

// CutIndex is the start of an index: at most LessonLimit lines of its lessons section and IndexLimit lines in all,
// each of at most lineLimit bytes and indexBytes in all; cut tells what was left out, outside the data.
func CutIndex(text string) (start, cut string) {
	lines, lessons := lessonsCut(strings.Split(strings.TrimRight(text, "\n"), "\n"))
	var kept, notes []string
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
	if lessons > LessonLimit {
		notes = append(notes, "deck cut "+strconv.Itoa(lessons-LessonLimit)+" more lines of the lessons section.")
	}
	switch {
	case len(kept) < len(lines):
		notes = append(notes, "deck cut "+strconv.Itoa(len(lines)-len(kept))+" more lines of this index.")
	case short:
		notes = append(notes, "deck shortened lines of this index longer than "+strconv.Itoa(lineLimit)+" bytes.")
	}
	return strings.Join(kept, "\n"), strings.Join(notes, " ")
}

// Share splits MemoryBudget between parts of these sizes in order of importance: each gets what it needs up to
// MemoryFloor, then what is left goes to them in order.
func Share(needs ...int) []int {
	out, left := make([]int, len(needs)), MemoryBudget
	for i, n := range needs {
		out[i] = min(n, MemoryFloor)
		left -= out[i]
	}
	for i, n := range needs {
		more := min(n-out[i], left)
		out[i] += more
		left -= more
	}
	return out
}

// CutBytes is the start of text of at most n bytes in whole lines, a first line cut when it alone is longer; cut
// tells whether anything was left out.
func CutBytes(text string, n int) (start string, cut bool) {
	if len(text) <= n {
		return text, false
	}
	end := strings.LastIndexByte(text[:n+1], '\n')
	if end <= 0 {
		return untrusted.Cut(text, max(n-len("…"), 0)), true
	}
	return text[:end], true
}

// Reached counts that these memory documents reached a turn or a search, in a transaction of its own after the read.
func Reached(ctx context.Context, st *store.Store, now time.Time, ids ...int64) {
	if len(ids) == 0 {
		return
	}
	err := st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		for _, id := range ids {
			if err := q.CountMemoryRead(ctx, db.CountMemoryReadParams{DocumentID: id, LastReadAt: store.Time(now)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("count the reads of memory", "err", err)
	}
}

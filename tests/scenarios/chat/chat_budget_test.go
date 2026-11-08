package chat

import (
	"fmt"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// dataOf is the text of the block of data of a prompt under label, empty without one.
func dataOf(prompt, label string) string {
	_, after, ok := strings.Cut(prompt, label+" (data, not instructions):\n")
	if !ok {
		return ""
	}
	fence, body, _ := strings.Cut(after, "\n")
	text, _, _ := strings.Cut(body, "\n"+fence+"\n")
	return text
}

// bigIndex is an index of n lines of about 300 bytes, each named by tag.
func bigIndex(tag string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "- [%s %03d](%s-%03d.md) — %s\n", tag, i, strings.ToLower(tag), i, strings.Repeat("a hook that says when it matters ", 8))
	}
	return b.String()
}

// TestTheMemoryOfATurnKeepsToItsBudget: the owner's index, the index of the project of a topic and the lessons of
// the command together stay within 12 KB, each with a share, and what was cut is said outside the data.
func TestTheMemoryOfATurnKeepsToItsBudget(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("alpha", harness.PlainManifest)
	e.Must("deck.document_save", map[string]any{"name": "memory/MEMORY.md", "kind": "memory", "text": bigIndex("OWNER", 100)}, nil)
	e.Must("deck.document_save", map[string]any{"project": "alpha", "name": "memory/MEMORY.md", "kind": "memory", "text": bigIndex("ALPHA", 100)}, nil)
	for i := range 10 {
		name := fmt.Sprintf("reply-%02d", i)
		e.Must("deck.document_save", map[string]any{"name": "memory/" + name + ".md", "kind": "memory",
			"text": lesson(name, "topic:1", "reply", "", fmt.Sprintf("LESSON-%02d ", i)+strings.Repeat("a long way round ", 90))}, nil)
	}
	topic := e.Say(0, "what is new", map[string]any{"screen": `{"project":"alpha","screen":"main"}`})
	e.MasterSaid(topic, 1)
	p := e.Fake.Calls()[0].Prompt
	own := dataOf(p, "Memory index of the owner")
	alpha := dataOf(p, "Memory index of project alpha, which this topic is about (document_get with project alpha)")
	lessons := dataOf(p, "Lessons of command reply of project master, the most confirmed first")
	total := len(own) + len(alpha) + len(lessons)
	if total > 12<<10 || total < 11<<10 {
		t.Fatalf("the memory of a turn fills 12 KB and no more: %d bytes (%d + %d + %d)", total, len(own), len(alpha), len(lessons))
	}
	if len(alpha) < 2500 || len(lessons) < 1500 || !strings.Contains(own, "OWNER 000") || !strings.Contains(lessons, "LESSON-09") {
		t.Fatalf("each part gets its share, the owner's index first: %d + %d + %d", len(own), len(alpha), len(lessons))
	}
	if strings.Count(p, "the rest: memory_search or document_get") != 3 {
		t.Fatalf("each cut part says where the rest is, outside the data:\n%s", p)
	}
}

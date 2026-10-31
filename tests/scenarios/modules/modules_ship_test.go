package modules_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestADocumentOfTheMasterAnAgentEditedTakesWhatDeckShipsNext: the master's edit merges into the next text deck ships,
// deck's text and a note where both changed it; a file deck ships no more goes.
func TestADocumentOfTheMasterAnAgentEditedTakesWhatDeckShipsNext(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()), harness.WithProvider(&harness.Fake{}))
	const name = "skills/widgets/SKILL.md"
	raw, err := os.ReadFile(filepath.Join(harness.Root, "master", name))
	if err != nil {
		t.Fatal(err)
	}
	shipped := string(raw)
	first := regexp.MustCompile(`(?m)^# .*$`).FindString(shipped)
	older := strings.Replace(shipped, first, "# An older title", 1)
	// The text an older deck shipped.
	e.PlantRevision("master", name, older)
	edit := strings.Replace(older, "# An older title", "# The master's title", 1) + "\nThe master's own line.\n"
	e.Must("deck.document_save", map[string]any{"project": "master", "name": name, "kind": "skill", "text": edit}, nil)
	// The edit as the master's: its saves go through an agent, this one came through the owner's API.
	e.SQL(`UPDATE document_revision SET author = 'master' WHERE author = 'owner' AND document_id =
		(SELECT d.id FROM document d JOIN project p ON p.id = d.project_id WHERE p.name = 'master' AND d.name = ?)`, name)
	// A skill an older deck shipped and this one does not.
	e.PlantRevision("master", "skills/gone/SKILL.md", "---\nname: gone\ndescription: A skill deck ships no more.\n---\n")

	e = e.Restart()
	var d struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": name}, &d)
	if d.Text != shipped+"\nThe master's own line.\n" {
		t.Fatalf("deck's text stands where both changed and the rest of the edit carries over: %.120q", d.Text)
	}
	var note string
	// Notes to the master wait for its next turn; no read lists them.
	if err := e.SQLRow(`SELECT text FROM feed WHERE for_master = 1 AND text LIKE '%' || ? || '%' ORDER BY id DESC LIMIT 1`, name).Scan(&note); err != nil {
		t.Fatalf("the master is told of the conflict: %v", err)
	}
	for _, want := range []string{"- # An older title", "+ # The master's title", "  " + first} {
		if !strings.Contains(note, want) {
			t.Fatalf("the note to the master holds the diff, %q missing: %s", want, note)
		}
	}
	e.Refused("deck.document_get", map[string]any{"project": "master", "name": "skills/gone/SKILL.md"}, 404, "")

	e = e.Restart()
	var again struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "master", "name": name}, &again)
	var notes int
	// Notes to the master wait for its next turn; no read lists them.
	if err := e.SQLRow(`SELECT count(*) FROM feed WHERE for_master = 1 AND text LIKE '%' || ? || '%'`, name).Scan(&notes); err != nil || notes != 1 || again.Text != d.Text {
		t.Fatalf("a second start with the same deck changes nothing and tells nothing again: %d notes, %v", notes, err)
	}
}

// TestTheOwnersEditOfTheMastersManifestMergesWithWhatDeckShips: the manifest of the master the owner edited takes the
// next manifest deck ships part by part; where both changed a part the owner's stands and the master hears of it.
func TestTheOwnersEditOfTheMastersManifestMergesWithWhatDeckShips(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()), harness.WithProvider(&harness.Fake{}))
	raw, err := os.ReadFile(filepath.Join(harness.Root, "master/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	shipped := string(raw)
	if !strings.Contains(shipped, `"icon": "sparkles"`) || !strings.Contains(shipped, `"ru": {`) {
		t.Fatal("the manifest of the master changed its icon or words; the test edits them")
	}
	older := strings.Replace(strings.Replace(shipped, `"icon": "sparkles"`, `"icon": "star"`, 1), `"ru": {`, `"ru": {"An old word": "Старое слово",`, 1)
	// The manifest an older deck shipped.
	e.PlantRevision("master", "manifest.json", older)
	edit := strings.Replace(strings.Replace(older, `"icon": "star"`, `"icon": "robot"`, 1), `"ru": {`, `"ru": {"The owner's word": "Слово владельца",`, 1)
	e.Must("deck.manifest_save", map[string]any{"project": "master", "manifest": edit, "reason": "the owner's edit"}, nil)

	e = e.Restart()
	var got struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "master"}, &got)
	var m struct {
		Icon string
		I18n map[string]map[string]string
	}
	if err := json.Unmarshal([]byte(got.Manifest), &m); err != nil {
		t.Fatal(err)
	}
	if m.Icon != "robot" || m.I18n["ru"]["The owner's word"] == "" || m.I18n["ru"]["An old word"] != "" || m.I18n["ru"]["Reply"] == "" {
		t.Fatalf("the owner's icon and word stay, deck's dropped word goes, its words stay: %s %v", m.Icon, m.I18n["ru"])
	}
	var note string
	// Notes to the master wait for its next turn; no read lists them.
	if err := e.SQLRow(`SELECT text FROM feed WHERE for_master = 1 AND text LIKE '%manifest.json%' ORDER BY id DESC LIMIT 1`).Scan(&note); err != nil ||
		!strings.Contains(note, "icon") || !strings.Contains(note, `"sparkles"`) {
		t.Fatalf("the master hears deck's icon did not take: %q %v", note, err)
	}
}

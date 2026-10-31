package owner

import (
	"deck2/tests/harness"
	"encoding/json"
	"testing"
)

// lightScreens are the most a whole read of each screen of deck may weigh on lightEnv, in bytes.
var lightScreens = map[string]int{"settings": 26_000, "files": 25_000, "spend": 10_000, "schedule": 10_000, "cockpit": 10_000, "chat": 10_000}

// TestTheScreensOfDeckStayLight: a whole read of each screen of deck stays within its weight on a working week, the
// tabs not shown, the pages and the light copies left out.
func TestTheScreensOfDeckStayLight(t *testing.T) {
	t.Parallel()
	e := harness.LightEnv(t)
	for screen, most := range lightScreens {
		raw, _ := json.Marshal(e.ScreenMessages("", screen, map[string]any{"lang": "ru"}))
		t.Logf("%s: %d bytes", screen, len(raw))
		if len(raw) > most {
			t.Errorf("a whole read of %s weighs %d bytes, more than %d", screen, len(raw), most)
		}
	}
	raw, _ := json.Marshal(e.ScreenMessages("notes", "main", nil))
	if t.Logf("notes main: %d bytes", len(raw)); len(raw) > 15_000 {
		t.Errorf("a screen of a module with 200 records weighs %d bytes", len(raw))
	}
}

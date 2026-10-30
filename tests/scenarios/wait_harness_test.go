package scenarios

import (
	"os"
	"time"
)

// within is how long a wait of d lasts: four times as long under the race detector or with DECK_TEST_SLOW set.
func within(d time.Duration) time.Duration {
	if raceOn || os.Getenv("DECK_TEST_SLOW") != "" {
		return 4 * d
	}
	return d
}

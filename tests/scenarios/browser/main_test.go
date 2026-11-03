package browser

import (
	"flag"
	"testing"

	"deck2/tests/harness"
)

// parallel is how many tests of real browsers run at once unless -parallel says otherwise: each has its deck and its
// browser, and more than a few at once only wait on each other.
const parallel = "4"

func TestMain(m *testing.M) {
	harness.Main(m, func() {
		flag.Parse()
		set := false
		flag.Visit(func(f *flag.Flag) { set = set || f.Name == "test.parallel" })
		if !set {
			_ = flag.Set("test.parallel", parallel)
		}
	})
}

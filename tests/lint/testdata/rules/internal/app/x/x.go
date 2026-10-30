package x

import (
	clock "time"
)

func f() {
	_ = clock.Now()
	go f()
	_ = clock.Since(clock.Time{})
}

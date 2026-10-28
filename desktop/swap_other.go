//go:build !darwin

package main

import "os"

// swap puts a in place of b, b where a was.
func swap(a, b string) error {
	tmp := a + ".old"
	if err := os.Rename(b, tmp); err != nil {
		return err
	}
	if err := os.Rename(a, b); err != nil {
		_ = os.Rename(tmp, b)
		return err
	}
	return os.Rename(tmp, a)
}

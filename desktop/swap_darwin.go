package main

import "golang.org/x/sys/unix"

// swap exchanges two paths in one step, so no moment is without deck.app.
func swap(a, b string) error { return unix.RenamexNp(a, b, unix.RENAME_SWAP) }

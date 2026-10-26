//go:build !darwin && !windows

package main

import "errors"

func autostart() bool { return false }

func setAutostart(bool) error { return errors.New("start at login is not made for this system") }

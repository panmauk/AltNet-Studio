//go:build !windows

package main

import "errors"

// On non-Windows we'll wire equivalents (sudo-elevated edit of /etc/hosts,
// macOS uses the same path) when we ship for those platforms. For now
// stubs so the app still compiles.

func hostsEntryExistsImpl(name string) bool   { return false }
func installHostsEntryImpl(name string) error { return errors.New("not implemented on this OS") }
func removeHostsEntryImpl(name string) error  { return errors.New("not implemented on this OS") }

//go:build !windows && !linux

package main

// Stubs for OSes without autostart wired yet (macOS would use a
// LaunchAgent). Linux is implemented in autostart_linux.go.

func EnableAutoStart() error  { return nil }
func DisableAutoStart() error { return nil }
func AutoStartEnabled() bool  { return false }
func CleanupLegacyAutoStart() {}

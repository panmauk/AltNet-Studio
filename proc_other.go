//go:build !windows

package main

import "os/exec"

// hideWindow is a no-op off Windows: Linux/macOS don't pop console
// windows for child processes.
func hideWindow(cmd *exec.Cmd) {}

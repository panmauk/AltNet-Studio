//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW: the child runs without allocating a console, so no
// black command window flashes when AltNet Studio spawns the daemon or
// runs a PowerShell helper.
const createNoWindow = 0x08000000

// hideWindow makes a child process start without a visible console window.
// Call it on an *exec.Cmd before Run/Start/Output. No-op on non-Windows.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

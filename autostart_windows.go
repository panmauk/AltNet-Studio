//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

// On Windows we register the app under the per-user Run key so it
// launches without elevation. The value is the full quoted path to
// AltNet.exe; Windows runs it on every login.
const (
	runKeyPath    = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName  = "AltNet Studio"
	// legacyRunValueName is what the app called itself before the
	// 2026-05 rename. cleanupLegacyAutoStart deletes it on startup so
	// users don't end up with two Run entries (one pointing at a
	// no-longer-existent AltNet.exe, one at the new AltNetStudio.exe).
	legacyRunValueName = "AltNet"
)

// CleanupLegacyAutoStart removes the pre-rename Run-key entry if it
// exists. Safe to call on every startup — idempotent.
func CleanupLegacyAutoStart() {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(legacyRunValueName)
}

// EnableAutoStart writes the Run key entry. exePath is normally
// os.Executable(); we accept it as a parameter so callers can pass a
// known path (useful in tests or when the binary moved).
func EnableAutoStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// The quotes guard against spaces in the path (e.g. "Program Files").
	// --hidden tells main.go to start with the window invisible so the
	// boot launch is silent — the app just sits in the background.
	return k.SetStringValue(runValueName, `"`+exe+`" --hidden`)
}

// DisableAutoStart removes the Run key entry. A missing entry is not
// an error — that's the desired end state either way.
func DisableAutoStart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// AutoStartEnabled reports whether the Run entry is currently set.
func AutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValueName)
	return err == nil
}

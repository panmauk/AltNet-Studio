//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Linux autostart via the XDG autostart spec: a .desktop file in
// ~/.config/autostart is launched by the desktop session on login. This
// is the per-user, no-root equivalent of the Windows Run key. We pass
// --hidden so the boot launch is silent and the app just sits in the
// background (Wails honors -hidden / HideWindowOnClose on Linux too).
//
// Works across GNOME, KDE, XFCE, etc. — they all read ~/.config/autostart.

const autostartFileName = "altnet-studio.desktop"

func autostartPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "autostart")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(d, autostartFileName), nil
}

// EnableAutoStart writes the XDG autostart entry pointing at this binary.
func EnableAutoStart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	path, err := autostartPath()
	if err != nil {
		return err
	}
	// Quote Exec per the Desktop Entry spec so paths with spaces work.
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=AltNet Studio
Comment=AltNet node — keeps .alt sites reachable
Exec="%s" --hidden
Terminal=false
X-GNOME-Autostart-enabled=true
`, exe)
	return os.WriteFile(path, []byte(entry), 0o644)
}

// DisableAutoStart removes the autostart entry. Missing == success.
func DisableAutoStart() error {
	path, err := autostartPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AutoStartEnabled reports whether the autostart entry is present.
func AutoStartEnabled() bool {
	path, err := autostartPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// CleanupLegacyAutoStart is a no-op on Linux — there's no pre-rename
// entry to remove (the Windows Run key had one; XDG autostart didn't
// exist before this).
func CleanupLegacyAutoStart() {}

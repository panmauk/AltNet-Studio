//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// Why these live in their own file: editing the Windows hosts file
// requires admin elevation, and the trick (one-shot elevated PowerShell
// child) is OS-specific. The non-Windows stubs live in hosts_other.go
// so build tags keep this file unbuilt elsewhere.

func hostsFilePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

// hostsEntryExistsImpl reads the hosts file (no admin needed) and
// reports whether a `127.0.0.1 <name>` line is present. Used to drive
// the "Set up clean URL" vs "Remove" toggle in the UI.
func hostsEntryExistsImpl(name string) bool {
	if !validHostsName(name) {
		return false
	}
	f, err := os.Open(hostsFilePath())
	if err != nil {
		return false
	}
	defer f.Close()
	re := regexp.MustCompile(`^\s*127\.0\.0\.1\s+` + regexp.QuoteMeta(name) + `\s*(#.*)?$`)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if re.MatchString(sc.Text()) {
			return true
		}
	}
	return false
}

func installHostsEntryImpl(name string) error {
	if !validHostsName(name) {
		return errors.New("invalid host name")
	}
	if hostsEntryExistsImpl(name) {
		return nil // already there
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$Name = '%s'
$hosts = "$env:SystemRoot\System32\drivers\etc\hosts"
$content = Get-Content $hosts -Encoding ASCII -ErrorAction SilentlyContinue
if ($null -eq $content) { $content = @() }
$pattern = "^\s*127\.0\.0\.1\s+$([regex]::Escape($Name))\s*$"
$exists = $content | Where-Object { $_ -match $pattern }
if (-not $exists) {
    Add-Content -Path $hosts -Value ("127.0.0.1 " + $Name + "  # AltNet") -Encoding ASCII
}
`, name)
	return runElevatedScript(script)
}

func removeHostsEntryImpl(name string) error {
	if !validHostsName(name) {
		return errors.New("invalid host name")
	}
	if !hostsEntryExistsImpl(name) {
		return nil
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$Name = '%s'
$hosts = "$env:SystemRoot\System32\drivers\etc\hosts"
$content = Get-Content $hosts -Encoding ASCII -ErrorAction SilentlyContinue
if ($null -eq $content) { return }
$pattern = "^\s*127\.0\.0\.1\s+$([regex]::Escape($Name))\s*(#.*)?$"
$filtered = $content | Where-Object { $_ -notmatch $pattern }
Set-Content -Path $hosts -Value $filtered -Encoding ASCII
`, name)
	return runElevatedScript(script)
}

// validHostsName allows only the character set that real DNS labels
// allow (plus dots for sub-labels). The single quote in the PowerShell
// generator above would let a malicious name break out — locking the
// charset closes that hole.
func validHostsName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// runElevatedScript writes the PowerShell payload to a temp file, then
// spawns a second PowerShell that calls Start-Process with -Verb
// RunAs (the UAC prompt) on the script. -Wait makes us block until
// the elevated child finishes, so a successful return means the file
// edit actually happened.
func runElevatedScript(script string) error {
	f, err := os.CreateTemp("", "altnet-hosts-*.ps1")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Single quotes inside the inner command list are safe because we
	// control both ends — no user content goes through the outer
	// PowerShell, only the temp path (which we generated).
	wrapper := fmt.Sprintf(
		`Start-Process powershell -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','%s' -Verb RunAs -Wait`,
		tmpPath,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", wrapper)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("elevated powershell: %w (output: %s)", err, string(out))
	}
	return nil
}

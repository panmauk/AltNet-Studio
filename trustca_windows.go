//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// trustca_windows.go installs / removes / queries the AltNet local CA
// in the user's Windows trust store. We use the CurrentUser Root store
// (HKCU) — no admin elevation required, no impact on other accounts on
// the machine. PowerShell's Import-Certificate / Get-ChildItem on
// Cert:\CurrentUser\Root do the work; we shell out so we don't need to
// link the Windows CryptoAPI ourselves.

// trustedCASubject is the X.509 Subject CN we look for. Must match the
// `CommonName: "AltNet Local CA"` set in apps/altca.
const trustedCASubject = "AltNet Local CA"

// caTrustedImpl reports whether the AltNet CA is installed somewhere
// browsers will actually honor it. We check both the Root store (where
// self-signed CAs canonically belong) AND the intermediate CA store
// (`\CA`) — turns out Import-Certificate on some Windows builds drops
// our self-signed cert into `\CA` instead of `\Root`, and modern
// browsers (Chrome/Edge) trust it from there anyway. Also check the
// LocalMachine equivalents because some installers/policies end up
// putting it there.
func caTrustedImpl() bool {
	script := `
$stores = @(
    'Cert:\CurrentUser\Root',
    'Cert:\CurrentUser\CA',
    'Cert:\LocalMachine\Root',
    'Cert:\LocalMachine\CA'
)
$found = $false
foreach ($s in $stores) {
    $hit = Get-ChildItem -Path $s -ErrorAction SilentlyContinue |
        Where-Object { $_.Subject -match 'CN=AltNet Local CA' }
    if ($hit) { $found = $true; break }
}
if ($found) { 'yes' } else { 'no' }
`
	out, err := powershell(script)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "yes"
}

func installCATrustImpl(certPath string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("ca cert: %w", err)
	}
	if caTrustedImpl() {
		return nil // idempotent
	}
	// Import-Certificate into the CurrentUser Root store. No admin
	// needed; affects only the current Windows user.
	script := fmt.Sprintf(
		`Import-Certificate -FilePath '%s' -CertStoreLocation Cert:\CurrentUser\Root | Out-Null`,
		certPath,
	)
	if _, err := powershell(script); err != nil {
		return fmt.Errorf("import cert: %w", err)
	}
	return nil
}

func removeCATrustImpl() error {
	// Mirror the trust check: sweep every store we'd look in, so the
	// user doesn't get a half-removed state where the cert disappears
	// from Root but lingers in CA (or vice versa).
	script := `
$stores = @(
    'Cert:\CurrentUser\Root',
    'Cert:\CurrentUser\CA',
    'Cert:\LocalMachine\Root',
    'Cert:\LocalMachine\CA'
)
foreach ($s in $stores) {
    Get-ChildItem -Path $s -ErrorAction SilentlyContinue |
        Where-Object { $_.Subject -match 'CN=AltNet Local CA' } |
        ForEach-Object { Remove-Item -Path $_.PSPath -Force -ErrorAction SilentlyContinue }
}
`
	if _, err := powershell(script); err != nil {
		return fmt.Errorf("remove cert: %w", err)
	}
	return nil
}

// powershell runs script with -NoProfile + -Command and returns its
// stdout/stderr combined. Used for the cert-store edits because the
// alternative (linking crypt32.dll via syscall) is much more work for
// the same effect.
func powershell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), errors.New(strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

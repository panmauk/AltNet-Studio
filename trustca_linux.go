//go:build linux

package main

import (
	"fmt"
	"os"
)

// Linux CA trust via the system trust store. We drop the AltNet local CA
// into /usr/local/share/ca-certificates and run update-ca-certificates,
// which rebuilds /etc/ssl/certs. Crucially, WebKitGTK (both our own
// embedded webview and GNOME Web/Epiphany) validates TLS against this
// system store via GnuTLS — so once installed, https://name.alt shows a
// real padlock in WebKit-based browsers with no warning.
//
// Note: Chrome and Firefox keep their own NSS trust DBs and would need
// `certutil -d sql:~/.pki/nssdb` (Chrome) / a Firefox policy to trust it
// too. That's a follow-up; the system store covers WebKitGTK, which is
// what AltNet Studio itself uses.
//
// Editing /usr/local/share is root-only, so we elevate via pkexec
// (runRootScript, shared with nrpt_linux.go).

const systemCACertPath = "/usr/local/share/ca-certificates/altnet-local-ca.crt"

// caTrustedImpl reports whether our CA is installed in the system store.
// Presence of the .crt under /usr/local/share/ca-certificates means
// update-ca-certificates has folded it into the trust bundle.
func caTrustedImpl() bool {
	_, err := os.Stat(systemCACertPath)
	return err == nil
}

// installCATrustImpl copies the AltNet CA (PEM) into the system store and
// refreshes the bundle. Idempotent. certPath is the daemon's CA cert.
func installCATrustImpl(certPath string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("ca cert: %w", err)
	}
	if caTrustedImpl() {
		return nil
	}
	// update-ca-certificates only picks up files with a .crt extension
	// containing PEM; install -m644 places it with the right perms.
	script := fmt.Sprintf(`set -e
install -m 0644 %q %q
update-ca-certificates`, certPath, systemCACertPath)
	return runRootScript(script)
}

// removeCATrustImpl removes the CA from the system store and refreshes.
func removeCATrustImpl() error {
	if !caTrustedImpl() {
		return nil
	}
	script := fmt.Sprintf(`set -e
rm -f %q
update-ca-certificates --fresh`, systemCACertPath)
	return runRootScript(script)
}

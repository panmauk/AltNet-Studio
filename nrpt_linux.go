//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Linux `.alt` resolution.
//
// On Linux we can't reuse the Windows trick of binding 127.0.0.53:53 —
// that address is owned by systemd-resolved, the standard stub resolver
// on Ubuntu/Fedora/etc. Instead the daemon binds its resolver on a free
// loopback port (127.0.0.1:5354) and we drop a small systemd-resolved
// config that routes every *.alt query to it:
//
//	[Resolve]
//	DNS=127.0.0.1:5354
//	Domains=~alt
//
// `Domains=~alt` is a routing-only domain, so this DNS server is used
// *only* for .alt — all other names keep resolving normally. Apps reach
// it through nss-resolve (getaddrinfo), which is what browsers and curl
// use, so `http://name.alt/` just works once the daemon's gateway is up.
//
// Verified end-to-end: getent/curl resolve panmox.alt -> 127.0.0.1 and
// the local gateway serves the site fetched from the DHT.

const daemonDNSAddr = "127.0.0.1:5354"

const (
	resolvedDropinPath = "/etc/systemd/resolved.conf.d/altnet-alt.conf"
	resolvedDropin     = "[Resolve]\nDNS=127.0.0.1:5354\nDomains=~alt\n"
)

// nrptRuleInstalledImpl reports whether our resolved drop-in is present.
// Unprivileged (the file is world-readable), so we can skip the elevated
// install on subsequent launches.
func nrptRuleInstalledImpl() bool {
	b, err := os.ReadFile(resolvedDropinPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "127.0.0.1:5354") &&
		strings.Contains(string(b), "~alt")
}

// installNRPTRuleImpl writes the systemd-resolved drop-in and restarts
// resolved so it takes effect. Idempotent. Elevates via pkexec when not
// already running as root.
func installNRPTRuleImpl() error {
	if nrptRuleInstalledImpl() {
		return nil
	}
	script := fmt.Sprintf(`set -e
mkdir -p /etc/systemd/resolved.conf.d
cat > %s <<'EOF'
%sEOF
systemctl restart systemd-resolved`, resolvedDropinPath, resolvedDropin)
	return runRootScript(script)
}

// removeNRPTRuleImpl tears the drop-in down and restarts resolved.
// Used by uninstall / "stop being a node". Idempotent.
func removeNRPTRuleImpl() error {
	if !nrptRuleInstalledImpl() {
		return nil
	}
	script := fmt.Sprintf(`set -e
rm -f %s
systemctl restart systemd-resolved`, resolvedDropinPath)
	return runRootScript(script)
}

// runRootScript runs a bash script as root. If we're already root (e.g.
// a packaged daemon launched by a system service, or a dev box), it runs
// directly; otherwise it uses pkexec, which shows a graphical polkit
// authentication dialog — the Linux equivalent of the Windows UAC prompt.
func runRootScript(script string) error {
	if os.Geteuid() == 0 {
		cmd := exec.Command("bash", "-c", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("configuring .alt resolution: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("cannot configure .alt resolution: pkexec not found; " +
			"install polkit or run AltNet Studio with the privileges to edit " +
			"/etc/systemd/resolved.conf.d")
	}
	cmd := exec.Command("pkexec", "bash", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("configuring .alt resolution (pkexec): %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

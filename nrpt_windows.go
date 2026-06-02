//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// NRPT (Name Resolution Policy Table) is the Windows feature we use
// to route every `*.alt` DNS query at the local daemon. Once the rule
// is installed, any browser typing `something.alt` has its DNS query
// answered by the daemon's resolver on 127.0.0.53 — which returns
// 127.0.0.1, and the local gateway takes over from there.
//
// This is the proper alternative to the per-site hosts-file hack: one
// UAC prompt at install, then any .alt name resolves forever without
// further intervention.
//
// The rule is identified by its `-Namespace` value (".alt"). Windows
// stores NRPT rules in the registry under
// HKLM\SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig,
// but we never touch that directly — Add/Get/Remove-DnsClientNrptRule
// are the supported PowerShell entry points.

// nrptNamespace is the DNS suffix we capture. Leading dot is required
// by NRPT semantics for "this and every subdomain."
const nrptNamespace = ".alt"

// nrptServer is the loopback IP the daemon's DNS resolver binds to.
// Must match daemonDNSAddr.
const nrptServer = "127.0.0.53"

// daemonDNSAddr is where the daemon's DNS resolver listens on Windows.
// 127.0.0.53:53 is free on Windows, and the NRPT rule routes *.alt here.
const daemonDNSAddr = "127.0.0.53:53"

// nrptRuleInstalledImpl checks (without admin) whether an NRPT rule
// for .alt pointing at 127.0.0.53 is already present. Used to skip
// the UAC prompt on subsequent launches.
//
// We use Get-DnsClientNrptRule which is read-only and works without
// elevation. Empty / "no rules" output means not installed.
func nrptRuleInstalledImpl() bool {
	cmd := exec.Command("powershell", "-NoProfile",
		"-Command",
		`(Get-DnsClientNrptRule -ErrorAction SilentlyContinue | `+
			`Where-Object { $_.Namespace -eq '`+nrptNamespace+`' -and `+
			`$_.NameServers -contains '`+nrptServer+`' }) -ne $null`,
	)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "true")
}

// installNRPTRuleImpl spawns elevated PowerShell to add the rule.
// Idempotent: returns nil immediately if the rule is already there.
//
// Add-DnsClientNrptRule is persistent — the rule survives reboots
// and uninstalls of the daemon (the leftover rule wouldn't break
// anything; with no daemon listening on 127.0.0.53, .alt names just
// fail to resolve, which is the same as the no-app state).
func installNRPTRuleImpl() error {
	if nrptRuleInstalledImpl() {
		return nil
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
# Remove any stale rule for the same namespace pointing at a different
# server (e.g. an old 127.0.0.1 from a previous version). Idempotent.
Get-DnsClientNrptRule -ErrorAction SilentlyContinue |
    Where-Object { $_.Namespace -eq '%s' } |
    ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }
Add-DnsClientNrptRule -Namespace '%s' -NameServers '%s' | Out-Null
`, nrptNamespace, nrptNamespace, nrptServer)
	return runElevatedScript(script)
}

// removeNRPTRuleImpl tears down the rule. Used by uninstall and by
// DisableNodeMode if the user opts out of being a node entirely.
// Idempotent.
func removeNRPTRuleImpl() error {
	if !nrptRuleInstalledImpl() {
		return nil
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Get-DnsClientNrptRule -ErrorAction SilentlyContinue |
    Where-Object { $_.Namespace -eq '%s' } |
    ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }
`, nrptNamespace)
	return runElevatedScript(script)
}

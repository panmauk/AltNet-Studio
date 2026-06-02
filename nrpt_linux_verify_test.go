//go:build linux

package main

import (
	"os"
	"testing"
)

// TestLinuxAltResolution exercises the real installNRPTRuleImpl /
// nrptRuleInstalledImpl / removeNRPTRuleImpl on Linux. It needs root
// (writes /etc/systemd/resolved.conf.d and restarts resolved), so it
// skips when not root. Run: go test -run TestLinuxAltResolution -v .
func TestLinuxAltResolution(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to edit resolved config")
	}
	_ = removeNRPTRuleImpl()
	if nrptRuleInstalledImpl() {
		t.Fatal("rule still present after remove")
	}
	if err := installNRPTRuleImpl(); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if !nrptRuleInstalledImpl() {
		t.Fatal("rule not detected after install")
	}
	b, err := os.ReadFile(resolvedDropinPath)
	if err != nil {
		t.Fatalf("drop-in not written: %v", err)
	}
	t.Logf("drop-in written:\n%s", b)
	// Leave it installed so the follow-up daemon browse test can use it.
}

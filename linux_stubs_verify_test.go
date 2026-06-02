//go:build linux

package main

import (
	"os"
	"testing"
)

func TestLinuxAutoStart(t *testing.T) {
	_ = DisableAutoStart()
	if AutoStartEnabled() {
		t.Fatal("still enabled after disable")
	}
	if err := EnableAutoStart(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !AutoStartEnabled() {
		t.Fatal("not enabled after EnableAutoStart")
	}
	p, _ := autostartPath()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read desktop file: %v", err)
	}
	t.Logf("autostart entry at %s:\n%s", p, b)
	if err := DisableAutoStart(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if AutoStartEnabled() {
		t.Fatal("still enabled after final disable")
	}
}

func TestLinuxCATrust(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to edit system trust store")
	}
	cert := os.Getenv("ALTNET_TEST_CACERT")
	if cert == "" {
		t.Skip("set ALTNET_TEST_CACERT to a PEM cert path")
	}
	_ = removeCATrustImpl()
	if caTrustedImpl() {
		t.Fatal("still trusted after remove")
	}
	if err := installCATrustImpl(cert); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !caTrustedImpl() {
		t.Fatal("not trusted after install")
	}
	t.Log("CA installed into system store and detected")
	if err := removeCATrustImpl(); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

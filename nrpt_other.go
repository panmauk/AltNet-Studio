//go:build !windows && !linux

package main

// Cross-platform stubs for OSes where `.alt` resolution isn't wired yet
// (currently macOS — it would use scutil + /etc/resolver/<tld>). Until
// then daemonDNSAddr is empty, so the supervisor omits -dns and the
// daemon doesn't bind a resolver nothing routes to. Linux has a real
// implementation in nrpt_linux.go.

const daemonDNSAddr = ""

func nrptRuleInstalledImpl() bool { return false }
func installNRPTRuleImpl() error  { return nil }
func removeNRPTRuleImpl() error   { return nil }

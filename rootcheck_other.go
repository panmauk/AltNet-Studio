//go:build !linux

package main

// Non-Linux platforms don't require root: Windows binds 80 as a normal
// user and uses NRPT/cert-store APIs without elevation (or prompts UAC
// per-action). os.Geteuid() returns -1 on Windows anyway, so we hard-code
// the answer rather than misreport.

func notRunningAsRoot() bool { return false }

const rootRequirementMessage = ""

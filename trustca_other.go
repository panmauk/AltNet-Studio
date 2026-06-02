//go:build !windows && !linux

package main

import "errors"

// Stubs for OSes without CA trust wired yet (macOS would use
// `security add-trusted-cert`). Linux is implemented in trustca_linux.go.

func caTrustedImpl() bool                     { return false }
func installCATrustImpl(_ string) error       { return errors.New("ca-trust install not implemented on this OS") }
func removeCATrustImpl() error                { return errors.New("ca-trust remove not implemented on this OS") }

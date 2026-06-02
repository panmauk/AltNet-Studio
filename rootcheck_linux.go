//go:build linux

package main

import "os"

// On Linux the app needs root to do its core job: bind port 80 for the
// .alt web gateway, edit systemd-resolved for .alt name resolution, and
// install the local CA. Rather than ship setcap/polkit packaging for the
// beta, we require the whole app to run as root and say so loudly.

func notRunningAsRoot() bool { return os.Geteuid() != 0 }

const rootRequirementMessage = "AltNet Studio must be run as root on Linux.\n\n" +
	"Root is required to:\n" +
	"  • bind port 80 so .alt sites open at http://name.alt/\n" +
	"  • configure systemd-resolved to resolve .alt names\n" +
	"  • install AltNet's local certificate authority\n\n" +
	"Quit, then relaunch from a terminal with:\n\n" +
	"    sudo -E ./AltNetStudio\n\n" +
	"Without root, .alt sites will NOT load and \"Be a node\" will fail."

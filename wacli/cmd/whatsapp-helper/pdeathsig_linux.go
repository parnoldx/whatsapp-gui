//go:build linux

package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// dieWithParent stops the daemon outliving the GUI that spawned it. Stdin EOF
// already covers the common case, but a wacli child that blocks on the store
// lock keeps us out of the read loop long enough to strand the process.
func dieWithParent() {
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGTERM), 0, 0, 0); err != nil {
		return
	}
	if os.Getppid() == 1 { // parent died before the prctl landed
		os.Exit(0)
	}
}

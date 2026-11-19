//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartSignals request a restart without dropping traffic.
var restartSignals = []os.Signal{syscall.SIGUSR2}

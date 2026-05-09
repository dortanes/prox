//go:build !windows

package plugin

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the plugin in its own process group
// so terminal signals don't kill it prematurely.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

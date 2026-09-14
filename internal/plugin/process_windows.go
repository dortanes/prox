//go:build windows

package plugin

import "os/exec"

// setProcessGroup is a no-op on Windows — process group isolation
// is not supported via SysProcAttr.Setpgid.
func setProcessGroup(cmd *exec.Cmd) {}

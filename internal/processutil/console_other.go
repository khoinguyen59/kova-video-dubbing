//go:build !windows

package processutil

import "os/exec"

// HideConsole is intentionally a no-op outside Windows.
func HideConsole(_ *exec.Cmd) {}

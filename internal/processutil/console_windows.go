//go:build windows

package processutil

import (
	"os/exec"
	"syscall"
)

// HideConsole prevents a console-program child such as ffmpeg.exe from
// flashing a Command Prompt window above the Kova desktop application.
// Kova captures the command output itself, so the child never needs a visible
// terminal.
func HideConsole(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	// CREATE_NO_WINDOW: do not allocate a new console for console executables.
	cmd.SysProcAttr.CreationFlags |= 0x08000000
}

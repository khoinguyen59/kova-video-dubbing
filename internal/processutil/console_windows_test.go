//go:build windows

package processutil

import (
	"os/exec"
	"testing"
)

func TestHideConsoleSetsWindowsProcessFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo", "kova")
	HideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideConsole did not hide the child console window")
	}
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("HideConsole did not set CREATE_NO_WINDOW")
	}
}

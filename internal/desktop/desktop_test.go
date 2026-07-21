package desktop

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func TestKovaDesktopShellKeepsSidebarAndWorkspaceUsableAtDesktopWidth(t *testing.T) {
	navigation := container.NewStack(canvas.NewRectangle(color.Transparent))
	workspace := canvas.NewRectangle(color.Transparent)
	bottom := container.NewPadded(widget.NewLabel("Kova ready"))
	shell := createKovaDesktopShell(navigation, workspace, bottom)
	shell.Resize(fyne.NewSize(1280, 820))

	if navigation.Size().Width < 320 {
		t.Fatalf("sidebar width = %f, want at least 320", navigation.Size().Width)
	}
	if workspace.Size().Width < 800 {
		t.Fatalf("workspace width = %f, want a usable center pane", workspace.Size().Width)
	}
	if bottom.Size().Width < 1000 {
		t.Fatalf("bottom status width = %f, want it to span the window", bottom.Size().Width)
	}
}

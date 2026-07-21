// KOVA's Wails entrypoint lives beside the existing Go module so that the
// Fyne desktop entrypoint can remain available during the migration. Build
// this target with `wails build` or `go build -tags production .`; the legacy application is
// still built from ./cmd/desktop.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"kova/config"
	"kova/log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"go.uber.org/zap"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

// useWailsWorkingDirectory makes a double-clicked desktop executable resolve
// ./config and ./bin from its portable app directory. Development builds are
// placed in <project>/build, so they deliberately reuse the tracked project
// root when it is available.
func useWailsWorkingDirectory() error {
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}
	workingDirectory := filepath.Dir(executablePath)
	projectDirectory := filepath.Dir(workingDirectory)
	if _, err := os.Stat(filepath.Join(projectDirectory, "config", "config.toml")); err == nil {
		workingDirectory = projectDirectory
	}
	return os.Chdir(workingDirectory)
}

func main() {
	if err := useWailsWorkingDirectory(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cannot initialize KOVA working directory: %v\n", err)
		return
	}

	log.InitLogger()
	defer log.GetLogger().Sync()

	// Make the Vite output directory the asset-server root explicitly. This
	// removes ambiguity when Wails resolves a nested go:embed tree at runtime.
	frontendDist, err := fs.Sub(frontendAssets, "frontend/dist")
	if err != nil {
		log.GetLogger().Error("KOVA frontend assets are unavailable", zap.Error(err))
		return
	}

	if !config.LoadConfig() {
		if err := config.SaveConfig(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "cannot create Kova configuration: %v\n", err)
			return
		}
	}

	app := NewApp()
	if err := wails.Run(&options.App{
		Title:            "KOVA · Video Localization Studio",
		Width:            1440,
		Height:           900,
		MinWidth:         1120,
		MinHeight:        720,
		DisableResize:    false,
		Fullscreen:       false,
		Frameless:        false,
		BackgroundColour: &options.RGBA{R: 10, G: 18, B: 34, A: 255},
		AssetServer:      &assetserver.Options{Assets: frontendDist},
		OnStartup:        app.Startup,
		OnShutdown:       app.Shutdown,
		Bind:             []interface{}{app},
	}); err != nil {
		log.GetLogger().Error("Kova Wails desktop stopped", zap.Error(err))
	}
}

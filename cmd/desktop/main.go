package main

import (
	"fmt"
	"go.uber.org/zap"
	"kova/config"
	"kova/internal/desktop"
	"kova/internal/server"
	"kova/log"
	"os"
	"path/filepath"
)

func usePortableWorkingDirectory() error {
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}

	return os.Chdir(portableWorkingDirectory(executablePath))
}

func portableWorkingDirectory(executablePath string) string {
	executableDir := filepath.Dir(executablePath)
	workingDir := executableDir

	// Development builds are written to <project>/build. Reuse the tracked
	// project configuration when it is present; a distributed executable falls
	// back to its own directory and creates config/config.toml beside itself.
	projectDir := filepath.Dir(executableDir)
	if _, err := os.Stat(filepath.Join(projectDir, "config", "config.toml")); err == nil {
		workingDir = projectDir
	}

	return workingDir
}

func main() {
	if err := usePortableWorkingDirectory(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cannot initialize desktop working directory: %v\n", err)
		os.Exit(1)
	}

	log.InitLogger()
	defer log.GetLogger().Sync()

	if !config.LoadConfig() {
		// Create a portable Kova configuration beside the executable.
		err := config.SaveConfig()
		if err != nil {
			log.GetLogger().Error("Failed to save Kova configuration", zap.Error(err))
			os.Exit(1)
		}
	}
	go func() {
		if err := server.StartBackend(); err != nil {
			log.GetLogger().Error("Failed to start Kova server", zap.Error(err))
			os.Exit(1)
		}
	}()
	config.ConfigBackup = config.Conf
	desktop.Show()
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPortableWorkingDirectoryUsesProjectConfigForDevelopmentBuild(t *testing.T) {
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[app]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	executablePath := filepath.Join(projectDir, "build", "Kova-Desktop-Windows-x64.exe")
	if got := portableWorkingDirectory(executablePath); got != projectDir {
		t.Fatalf("portableWorkingDirectory() = %q, want project directory %q", got, projectDir)
	}
}

func TestPortableWorkingDirectoryFallsBackToExecutableDirectory(t *testing.T) {
	executableDir := t.TempDir()
	executablePath := filepath.Join(executableDir, "Kova-Desktop-Windows-x64.exe")

	if got := portableWorkingDirectory(executablePath); got != executableDir {
		t.Fatalf("portableWorkingDirectory() = %q, want executable directory %q", got, executableDir)
	}
}

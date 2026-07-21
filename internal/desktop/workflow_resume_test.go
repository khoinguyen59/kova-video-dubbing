package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestWorkflowTaskIDReturnsNewestPersistedJob(t *testing.T) {
	root := t.TempDir()
	older := filepath.Join(root, "older-job")
	newer := filepath.Join(root, "newer-job")
	for _, dir := range []string{older, newer} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create task directory %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workflow_state.json"), []byte(`{"task_id":"test"}`), 0o644); err != nil {
			t.Fatalf("write workflow state for %s: %v", dir, err)
		}
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(older, "workflow_state.json"), base, base); err != nil {
		t.Fatalf("set old timestamp: %v", err)
	}
	if err := os.Chtimes(filepath.Join(newer, "workflow_state.json"), base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatalf("set new timestamp: %v", err)
	}

	got, err := latestWorkflowTaskID(root)
	if err != nil {
		t.Fatalf("latestWorkflowTaskID() error = %v", err)
	}
	if got != "newer-job" {
		t.Fatalf("latestWorkflowTaskID() = %q, want newer-job", got)
	}
}

func TestLatestWorkflowTaskIDRejectsEmptyDirectory(t *testing.T) {
	_, err := latestWorkflowTaskID(t.TempDir())
	if err == nil {
		t.Fatal("latestWorkflowTaskID() succeeded without any persisted job")
	}
}

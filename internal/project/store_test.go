package project

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStagesRequireApprovalAndInvalidateDownstreamRuns(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project, err := store.CreateProject(ctx, "Video thử nghiệm", "vi")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartStage(ctx, project.ID, StageTranslation); !errors.Is(err, ErrPrerequisite) {
		t.Fatalf("StartStage(translation) error = %v, want ErrPrerequisite", err)
	}
	source, err := store.StartStage(ctx, project.ID, StageSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkReviewRequired(ctx, source.ID, "stage.source.ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApproveStage(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	translation, err := store.StartStage(ctx, project.ID, StageTranslation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkReviewRequired(ctx, translation.ID, "stage.translation.ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApproveStage(ctx, translation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartStage(ctx, project.ID, StageSource); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.StageRuns[1].Status != StatusStale {
		t.Fatalf("translation status = %q, want %q", snapshot.StageRuns[1].Status, StatusStale)
	}
}

func TestArtifactsAreRelativeToTheProjectRoot(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	project, err := store.CreateProject(ctx, "Artifact safety", "vi")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartStage(ctx, project.ID, StageSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArtifact(ctx, Artifact{ProjectID: project.ID, StageRunID: run.ID, Kind: "source_video", Path: "../secret.mp4"}); err == nil {
		t.Fatal("expected unsafe artifact path to fail")
	}
	artifact, err := store.CreateArtifact(ctx, Artifact{ProjectID: project.ID, StageRunID: run.ID, Kind: "source_video", Path: "source/source.mp4", Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Path != "source/source.mp4" {
		t.Fatalf("artifact path = %q", artifact.Path)
	}
}

func TestProjectStoresLegacyWorkflowTaskIDWithoutSecrets(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateProject(ctx, "Workflow link", "vi")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetWorkflowTaskID(ctx, created.ID, "kova-source-123")
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowTaskID != "kova-source-123" {
		t.Fatalf("WorkflowTaskID = %q", updated.WorkflowTaskID)
	}
}

func TestDeleteProjectRemovesTimelineAndArtifacts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateProject(ctx, "Delete me", "vi")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartStage(ctx, created.ID, StageSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArtifact(ctx, Artifact{ProjectID: created.ID, StageRunID: run.ID, Kind: "source_review_draft", Path: "projects/test/draft.txt", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(ctx, created.ID); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if _, err := store.Snapshot(ctx, created.ID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("deleted snapshot error = %v, want ErrProjectNotFound", err)
	}
	projects, err := store.ListProjects(ctx)
	if err != nil || len(projects) != 0 {
		t.Fatalf("remaining projects = %+v, err=%v", projects, err)
	}
}

func TestSetFailureDetailReplacesGenericBackgroundFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateProject(ctx, "Failure details", "vi")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartStage(ctx, created.ID, StageSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailStage(ctx, run.ID, "worker_failed"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetFailureDetail(ctx, run.ID, "yt-dlp executable was not found")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusFailed || updated.FailureCode != "yt-dlp executable was not found" {
		t.Fatalf("updated run = %+v", updated)
	}
}

func TestOutputsCannotStartUntilRenderIsReviewedAndApproved(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateProject(ctx, "Five stages", "vi")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []Stage{StageSource, StageTranslation, StageDubbingAudio} {
		run, err := store.StartStage(ctx, created.ID, stage)
		if err != nil {
			t.Fatalf("StartStage(%s): %v", stage, err)
		}
		if _, err := store.MarkReviewRequired(ctx, run.ID, "stage.ready"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApproveStage(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.StartStage(ctx, created.ID, StageOutputs); !errors.Is(err, ErrPrerequisite) {
		t.Fatalf("StartStage(outputs) before render = %v, want ErrPrerequisite", err)
	}
	render, err := store.StartStage(ctx, created.ID, StageRender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkReviewRequired(ctx, render.ID, "stage.ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApproveStage(ctx, render.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartStage(ctx, created.ID, StageOutputs); err != nil {
		t.Fatalf("StartStage(outputs) after render approval: %v", err)
	}
}

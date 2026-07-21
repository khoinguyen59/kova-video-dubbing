package desktop

import (
	"testing"

	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestCreateWorkflowTabsBuildsFivePersistentSidebarPages(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova workflow test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	pages := CreateWorkflowTabs(w)
	if len(pages) != 5 {
		t.Fatalf("workflow page count = %d, want 5", len(pages))
	}
	for i, page := range pages {
		if page == nil {
			t.Fatalf("workflow page %d is nil", i+1)
		}
	}
}

func TestWorkflowUsesExplicitStageStartsAndDefaultsToMP4(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova workflow action test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	workflow := CreateWorkflow(w)
	if workflow.stage == nil {
		t.Fatal("staged workflow runner is nil")
	}
	if workflow.stage.sm.embedSubtitle != "horizontal" {
		t.Fatalf("default embed mode = %q, want horizontal so a normal run produces MP4", workflow.stage.sm.embedSubtitle)
	}
	if workflow.stage.sourceStartButton.Disabled() {
		t.Fatal("source start must be available before any network action")
	}
	for name, button := range map[string]*widget.Button{
		"translation":   workflow.stage.translationStartButton,
		"dubbing audio": workflow.stage.dubbingStartButton,
		"dubbing video": workflow.stage.dubbingVideoStartButton,
		"render":        workflow.stage.renderStartButton,
	} {
		if !button.Disabled() {
			t.Fatalf("%s start is enabled before the preceding review, want disabled", name)
		}
	}
	if workflow.PersistentActionBar() == nil {
		t.Fatal("persistent stage guidance is nil")
	}
	if len(workflow.runner.buttons) != 0 {
		t.Fatalf("legacy one-click controls leaked into main workflow: %d", len(workflow.runner.buttons))
	}
}

func TestWorkflowReviewGatesOpenOnlyAfterApproval(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova staged workflow gate test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	runner := newStagedWorkflowRunner(w, NewSubtitleManager(w))
	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "awaiting_source_review"})
	if runner.sourceReviewButton.Disabled() {
		t.Fatal("source review must expose an explicit check/approve CTA at awaiting_source_review")
	}
	if runner.sourceApproveButton.Disabled() {
		t.Fatal("source review approval must be available at awaiting_source_review")
	}
	if runner.sourceEditor.Disabled() {
		t.Fatal("source SRT editor must be enabled while the user is reviewing it")
	}
	if !runner.translationStartButton.Disabled() {
		t.Fatal("translation must remain disabled until source approval")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "source_approved"})
	if runner.translationStartButton.Disabled() {
		t.Fatal("translation start must open only after source approval")
	}
	if !runner.sourceEditor.Disabled() {
		t.Fatal("source SRT editor must close after approval to preserve the reviewed revision")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "awaiting_translation_review"})
	if runner.translationReviewButton.Disabled() {
		t.Fatal("translation review must expose an explicit check/approve CTA at awaiting_translation_review")
	}
	if runner.translationApproveButton.Disabled() {
		t.Fatal("translation approval must be available at awaiting_translation_review")
	}
	if runner.translatedEditor.Disabled() {
		t.Fatal("translated SRT editor must be enabled while the user is reviewing it")
	}
	if !runner.renderStartButton.Disabled() {
		t.Fatal("render must remain disabled until translation approval")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "translation_approved"})
	if runner.renderStartButton.Disabled() {
		t.Fatal("render should be available after translation approval when dubbing is off")
	}

	runner.sm.voiceoverEnabled = true
	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "translation_approved", CanStart: map[string]bool{"dubbing_audio": true, "render": true}})
	if runner.dubbingStartButton.Disabled() {
		t.Fatal("audio generation must open after translation approval when fixed voice is enabled")
	}
	if !runner.dubbingVideoStartButton.Disabled() || !runner.dubbingVideoApproveButton.Disabled() {
		t.Fatal("video mux/review must remain closed before audio review")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "awaiting_dubbing_audio_review", CanStart: map[string]bool{"dubbing_audio_approve": true}})
	if runner.dubbingApproveButton.Disabled() {
		t.Fatal("audio approval must be available after reviewable audio is generated")
	}
	if !runner.dubbingVideoStartButton.Disabled() {
		t.Fatal("video mux must remain closed until audio approval")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "dubbing_audio_approved", CanStart: map[string]bool{"dubbing_video": true}})
	if runner.dubbingVideoStartButton.Disabled() {
		t.Fatal("video mux must open only after audio approval")
	}
	if !runner.renderStartButton.Disabled() {
		t.Fatal("render must remain closed until dubbed video approval")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "awaiting_dubbing_video_review", CanStart: map[string]bool{"dubbing_video_approve": true}})
	if runner.dubbingVideoApproveButton.Disabled() {
		t.Fatal("video approval must be available after mux output is generated")
	}

	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-1", CurrentStage: "dubbing_video_approved", CanStart: map[string]bool{"render": true}})
	if runner.renderStartButton.Disabled() {
		t.Fatal("render must open after the user approves dubbed video")
	}
}

func TestWorkflowNewJobReopensSourceWithoutDeletingThePriorTask(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova new job test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	runner := newStagedWorkflowRunner(w, NewSubtitleManager(w))
	runner.applySnapshot(WorkflowSnapshot{TaskID: "completed-job", CurrentStage: "completed", ProcessPercent: 100})
	if !runner.sourceStartButton.Disabled() {
		t.Fatal("source start should remain closed while a completed job is active")
	}
	runner.NewJob()
	_, taskID, _, _ := runner.current()
	if taskID != "" {
		t.Fatalf("NewJob retained task ID %q", taskID)
	}
	if runner.sourceStartButton.Disabled() {
		t.Fatal("NewJob must re-enable the explicit source start button")
	}
	if runner.translationStartButton.Disabled() == false || runner.renderStartButton.Disabled() == false {
		t.Fatal("NewJob must keep later stages closed until their preceding reviews")
	}
}

func TestWorkflowUsesServerRetryGateAfterFailure(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova staged workflow retry test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	runner := newStagedWorkflowRunner(w, NewSubtitleManager(w))

	runner.applySnapshot(WorkflowSnapshot{
		TaskID:       "job-translation-retry",
		CurrentStage: "failed",
		CanStart:     map[string]bool{"translation": true},
	})
	if runner.translationStartButton.Disabled() {
		t.Fatal("server-approved translation retry must be enabled after a failed translation")
	}

	runner.sm.voiceoverEnabled = true
	runner.applySnapshot(WorkflowSnapshot{
		TaskID:       "job-dubbing-retry",
		CurrentStage: "failed",
		CanStart:     map[string]bool{"dubbing_audio": true},
	})
	if runner.dubbingStartButton.Disabled() {
		t.Fatal("server-approved dubbing retry must be enabled after a failed dubbing stage")
	}

	runner.applySnapshot(WorkflowSnapshot{
		TaskID:       "job-render-retry",
		CurrentStage: "failed",
		CanStart:     map[string]bool{"render": true},
	})
	if runner.renderStartButton.Disabled() {
		t.Fatal("server-approved render retry must be enabled after a failed render stage")
	}
}

func TestWorkflowDisablesSkipWhileDubbingIsActive(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	w := a.NewWindow("Kova staged workflow skip gate test")
	defer w.Close()

	SetGlobalThemeManager(NewThemeManager(a, w))
	runner := newStagedWorkflowRunner(w, NewSubtitleManager(w))
	runner.applySnapshot(WorkflowSnapshot{
		TaskID:       "job-dubbing-active",
		CurrentStage: "dubbing_audio_running",
		CanStart:     map[string]bool{"dubbing_skip": false},
	})
	if !runner.dubbingSkipButton.Disabled() {
		t.Fatal("skip dubbing must remain disabled while the dubbing worker is active")
	}

	// The same state must be safe against an older backend that omits the new
	// explicit gate from its snapshot.
	runner.applySnapshot(WorkflowSnapshot{TaskID: "job-dubbing-active", CurrentStage: "dubbing_audio_running"})
	if !runner.dubbingSkipButton.Disabled() {
		t.Fatal("legacy fallback must not allow skipping an active dubbing worker")
	}
}

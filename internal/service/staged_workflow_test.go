package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kova/internal/dto"
	"kova/internal/service/dubbing"
	"kova/internal/storage"
	"kova/internal/types"
)

const validReviewSRT = `1
00:00:00,000 --> 00:00:01,000
Hello from Kova.
`

func seedWorkflowForTest(t *testing.T, stage string) *subtitleWorkflow {
	t.Helper()
	taskID := "workflow_stage_test_" + t.Name()[len("Test"):]
	workflow := &subtitleWorkflow{
		TaskID:         taskID,
		TaskBasePath:   t.TempDir(),
		URL:            "https://youtu.be/uDVoZ39mONk",
		OriginLanguage: "en",
		TargetLanguage: "vi",
		UserLanguage:   "vi",
		EmbedType:      "horizontal",
		CurrentStage:   stage,
		Message:        "test",
		SourceApproved: stage == workflowSourceApproved || stage == workflowTranslationApproved,
	}
	workflowSessions.Store(taskID, workflow)
	storage.SubtitleTasks.Store(taskID, &types.SubtitleTask{TaskId: taskID, Status: types.SubtitleTaskStatusProcessing})
	t.Cleanup(func() {
		workflowSessions.Delete(taskID)
		storage.SubtitleTasks.Delete(taskID)
	})
	return workflow
}

func writeWorkflowFixture(t *testing.T, workflow *subtitleWorkflow, name, content string) string {
	t.Helper()
	path := filepath.Join(workflow.TaskBasePath, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestNormalizeOmniVoiceReferenceKeepsRemoteProfileOpaque(t *testing.T) {
	reference, isProfile, err := normalizeOmniVoiceReference(" profile:f4d5829e2cc448058c0d6195c368beba ")
	if err != nil {
		t.Fatalf("normalizeOmniVoiceReference(profile) error = %v", err)
	}
	if !isProfile || reference != "profile:f4d5829e2cc448058c0d6195c368beba" {
		t.Fatalf("profile reference = (%q, %v), want opaque profile", reference, isProfile)
	}

	local, isProfile, err := normalizeOmniVoiceReference("local:C:/voices/reference.wav")
	if err != nil {
		t.Fatalf("normalizeOmniVoiceReference(local) error = %v", err)
	}
	if isProfile || local != "C:/voices/reference.wav" {
		t.Fatalf("local reference = (%q, %v), want local file", local, isProfile)
	}

	if _, isProfile, err := normalizeOmniVoiceReference("profile:   "); err == nil || !isProfile {
		t.Fatalf("empty profile = (%v, %v), want opaque-profile validation error", err, isProfile)
	}
}

func TestHybridAutoSourceFallsBackToSTTWhenOptionalOCRIsUnavailable(t *testing.T) {
	method, warning, err := resolveSourceMethodWithOCRFallback(
		sourceMethodSpeechToTextAndOCR,
		reviewModeAuto,
		true,
		func() error { return errors.New("No module named 'paddle'") },
	)
	if err != nil {
		t.Fatalf("hybrid auto fallback returned error: %v", err)
	}
	if method != sourceMethodSpeechToText {
		t.Fatalf("method = %q, want STT fallback", method)
	}
	if !strings.Contains(warning, "Speech-to-Text") {
		t.Fatalf("fallback warning = %q, want an actionable STT notice", warning)
	}
}

func TestNormalizeWorkflowSourceCookieBrowser(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{"", sourceCookieBrowserAuto},
		{"AUTO", sourceCookieBrowserAuto},
		{"chrome", sourceCookieBrowserChrome},
		{" Edge ", sourceCookieBrowserEdge},
		{"none", sourceCookieBrowserNone},
	} {
		got, err := normalizeWorkflowSourceCookieBrowser(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("normalizeWorkflowSourceCookieBrowser(%q) = (%q, %v), want (%q, nil)", test.raw, got, err, test.want)
		}
	}
	if _, err := normalizeWorkflowSourceCookieBrowser("firefox"); err == nil {
		t.Fatal("unsupported browser must be rejected")
	}
}

func TestOCROnlyAndOptOutStillFailWhenOCRIsUnavailable(t *testing.T) {
	for _, request := range []struct {
		name, method, mode string
		fallback           bool
	}{
		{name: "ocr only", method: sourceMethodVisualOCR, mode: reviewModeAuto, fallback: true},
		{name: "hybrid manual", method: sourceMethodSpeechToTextAndOCR, mode: reviewModeManual, fallback: true},
		{name: "hybrid opt out", method: sourceMethodSpeechToTextAndOCR, mode: reviewModeAuto, fallback: false},
	} {
		t.Run(request.name, func(t *testing.T) {
			_, _, err := resolveSourceMethodWithOCRFallback(
				request.method,
				request.mode,
				request.fallback,
				func() error { return errors.New("PaddleOCR unavailable") },
			)
			if err == nil || !strings.Contains(err.Error(), "Visual OCR is not ready") {
				t.Fatalf("error = %v, want explicit OCR preflight failure", err)
			}
		})
	}
}

func TestWorkflowSnapshotExposesSourceWarning(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowSourceRunning)
	workflow.SourceWarning = "Visual OCR skipped; continuing with Speech-to-Text."
	if warning := workflowSnapshot(workflow).SourceWarning; warning != workflow.SourceWarning {
		t.Fatalf("SourceWarning = %q, want %q", warning, workflow.SourceWarning)
	}
}

func TestWorkflowRequiresSourceApprovalBeforeTranslation(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitSourceReview)
	writeWorkflowFixture(t, workflow, types.SubtitleTaskVideoFileName, "source video")

	if _, err := (Service{}).StartWorkflowTranslation(workflow.TaskID); err == nil {
		t.Fatal("StartWorkflowTranslation() accepted an unapproved source SRT")
	}

	state, err := (Service{}).ApproveWorkflowStage(workflow.TaskID, "source")
	if err != nil {
		t.Fatalf("ApproveWorkflowStage(source): %v", err)
	}
	if state.CurrentStage != workflowSourceApproved || !state.CanStart["translation"] {
		t.Fatalf("source approval state = %#v, want translation enabled", state)
	}
}

func TestSavingSourceSRTInvalidatesAllDownstreamApprovals(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowTranslationApproved)
	workflow.SourceApproved = true
	workflow.TranslationApproved = true
	workflow.DubbingRequested = true
	workflow.DubbingAudioApproved = true
	workflow.DubbingVideoApproved = true
	workflow.DubbingApproved = true
	writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, validReviewSRT)
	target := writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, validReviewSRT)
	writeWorkflowFixture(t, workflow, types.TtsResultAudioFileName, "old audio")

	state, err := (Service{}).UpdateWorkflowSubtitle(workflow.TaskID, "source", validReviewSRT)
	if err != nil {
		t.Fatalf("UpdateWorkflowSubtitle(source): %v", err)
	}
	if state.CurrentStage != workflowAwaitSourceReview || state.CanStart["translation"] {
		t.Fatalf("source edit state = %#v, want renewed source review", state)
	}
	workflow.mu.Lock()
	approved := workflow.SourceApproved || workflow.TranslationApproved || workflow.DubbingRequested || workflow.DubbingAudioApproved || workflow.DubbingVideoApproved || workflow.DubbingApproved
	workflow.mu.Unlock()
	if approved {
		t.Fatal("source edit retained a downstream approval")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("stale translated SRT still exists after source edit: %v", err)
	}
}

func TestWorkflowRejectsMalformedSRTWithoutChangingReview(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitSourceReview)
	original := writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, validReviewSRT)
	if _, err := (Service{}).UpdateWorkflowSubtitle(workflow.TaskID, "source", "not an SRT"); err == nil {
		t.Fatal("UpdateWorkflowSubtitle accepted malformed SRT")
	}
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != validReviewSRT {
		t.Fatalf("malformed edit altered source file: %q", data)
	}
}

func TestSavingTranslatedSRTRebuildsBilingualArtifacts(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitTranslation)
	workflow.SourceApproved = true
	workflow.TranslationApproved = false
	writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, validReviewSRT)
	writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, `1
00:00:00,000 --> 00:00:01,000
Xin chào cũ.
`)
	writeWorkflowFixture(t, workflow, types.TtsResultAudioFileName, "stale audio")

	svc := Service{YouTubeSubtitleSrv: NewYouTubeSubtitleService()}
	state, err := svc.UpdateWorkflowSubtitle(workflow.TaskID, "translated", `1
00:00:00,000 --> 00:00:01,000
Xin chào Kova.
`)
	if err != nil {
		t.Fatalf("UpdateWorkflowSubtitle(translated): %v", err)
	}
	if state.CurrentStage != workflowAwaitTranslation {
		t.Fatalf("translated edit stage = %q, want %q", state.CurrentStage, workflowAwaitTranslation)
	}
	bilingual, err := os.ReadFile(filepath.Join(workflow.TaskBasePath, types.SubtitleTaskBilingualSrtFileName))
	if err != nil {
		t.Fatalf("read rebuilt bilingual SRT: %v", err)
	}
	for _, expected := range []string{"Hello from Kova.", "Xin chào Kova."} {
		if !strings.Contains(string(bilingual), expected) {
			t.Fatalf("rebuilt bilingual SRT missing %q: %s", expected, bilingual)
		}
	}
	if _, err := os.Stat(filepath.Join(workflow.TaskBasePath, types.TtsResultAudioFileName)); !os.IsNotExist(err) {
		t.Fatalf("stale dubbed audio still exists after translated edit: %v", err)
	}
}

func TestSavingTranslatedSRTRejectsRetimedCue(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitTranslation)
	workflow.SourceApproved = true
	writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, validReviewSRT)
	original := writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, validReviewSRT)

	svc := Service{YouTubeSubtitleSrv: NewYouTubeSubtitleService()}
	if _, err := svc.UpdateWorkflowSubtitle(workflow.TaskID, "translated", `1
00:00:00,100 --> 00:00:01,000
Xin chào.
`); err == nil {
		t.Fatal("UpdateWorkflowSubtitle accepted a retimed translated cue")
	}
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != validReviewSRT {
		t.Fatalf("failed translated edit altered saved SRT: %q", data)
	}
}

func TestExtractSourceSRTForReviewDoesNotTranslate(t *testing.T) {
	base := t.TempDir()
	vttData, err := os.ReadFile("test.vtt")
	if err != nil {
		t.Fatalf("read VTT fixture: %v", err)
	}
	vttPath := filepath.Join(base, "fixture.en.vtt")
	if err := os.WriteFile(vttPath, vttData, 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewYouTubeSubtitleService()
	origin, err := svc.extractSourceSRTForReview(&YoutubeSubtitleReq{
		TaskBasePath:   base,
		TaskId:         "review_fixture",
		OriginLanguage: "en",
		TargetLanguage: "vi",
		VttFile:        vttPath,
	})
	if err != nil {
		t.Fatalf("extractSourceSRTForReview(): %v", err)
	}
	if _, err := workflowSRTBlocks(origin); err != nil {
		t.Fatalf("source SRT invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, types.SubtitleTaskTargetLanguageSrtFileName)); !os.IsNotExist(err) {
		t.Fatalf("source stage unexpectedly wrote target SRT: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "output", types.SubtitleTaskOriginLanguageTextFileName)); err != nil {
		t.Fatalf("source stage did not write reviewable script: %v", err)
	}
}

func TestWorkflowSnapshotRequiresTranslationApprovalForDubbing(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitTranslation)
	writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, validReviewSRT)
	state := workflowSnapshot(workflow)
	if state.CanStart["dubbing"] || state.CanStart["render"] {
		t.Fatalf("unapproved translation enabled a later stage: %#v", state.CanStart)
	}
}

func TestWorkflowSnapshotRestoresCompletedProgressAndSourceURL(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowCompleted)
	workflow.TranslationApproved = true
	state := workflowSnapshot(workflow)
	if state.ProcessPercent != 100 {
		t.Fatalf("completed workflow progress = %d, want 100", state.ProcessPercent)
	}
	if state.SourceUrl != workflow.URL {
		t.Fatalf("snapshot source URL = %q, want %q", state.SourceUrl, workflow.URL)
	}
}

func TestWorkflowSnapshotSeparatesMediaDownloadFromScriptCreation(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowSourceRunning)
	workflow.updateSourceStep("download_audio", 100, "audio ready")
	workflow.updateSourceStep("download_video", 100, "video ready")
	workflow.SourceMethod = sourceMethodSpeechToText
	workflow.TranslationSteps = initialTranslationStepsFor(workflow.SourceMethod)
	workflow.updateTranslationStep("speech_to_text", 45, "segment 2/4", time.Time{})

	state := workflowSnapshot(workflow)
	if len(state.SourceSteps) != 2 {
		t.Fatalf("source step count = %d, want 2", len(state.SourceSteps))
	}
	byID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.SourceSteps {
		byID[step.ID] = step
	}
	if got := byID["download_audio"]; got.State != "completed" || got.Percent != 100 {
		t.Fatalf("audio phase = %#v, want completed 100", got)
	}
	if got := byID["download_video"]; got.State != "completed" || got.Percent != 100 {
		t.Fatalf("video phase = %#v, want completed 100", got)
	}
	translationByID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.TranslationSteps {
		translationByID[step.ID] = step
	}
	if got := translationByID["speech_to_text"]; got.State != "running" || got.Percent != 45 {
		t.Fatalf("STT phase = %#v, want running 45", got)
	}
}

func TestWorkflowSnapshotKeepsSeparateDubbingPhasesAndHeartbeat(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowDubbingAudioRunning)
	workflow.DubbingSteps = initialDubbingSteps()
	workflow.updateDubbingStep("prepare", 100, "approved SRT ready")
	workflow.updateDubbingStep("synthesize", 50, "speech block 11/22 is ready")

	state := workflowSnapshot(workflow)
	if state.UpdatedAt == "" {
		t.Fatal("dubbing progress did not update the workflow heartbeat")
	}
	byID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.DubbingSteps {
		byID[step.ID] = step
	}
	if got := byID["prepare"]; got.State != "completed" || got.Percent != 100 {
		t.Fatalf("prepare phase = %#v, want completed 100", got)
	}
	if got := byID["synthesize"]; got.State != "running" || got.Percent != 50 || got.Detail != "speech block 11/22 is ready" {
		t.Fatalf("synthesis phase = %#v, want running 50 with detail", got)
	}

	workflow.failActiveDubbingStep("gateway request timed out after 25 seconds")
	state = workflowSnapshot(workflow)
	for _, step := range state.DubbingSteps {
		if step.ID == "synthesize" && step.State != "failed" {
			t.Fatalf("synthesis phase after failure = %q, want failed", step.State)
		}
	}
}

func TestWorkflowSnapshotKeepsSeparateRenderPhasesAndMeasuredETA(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowRenderRunning)
	workflow.RenderSteps = initialRenderSteps()
	eta := time.Now().Add(2 * time.Minute)
	workflow.updateRenderStep("render_preflight", 100, "media tools ready", time.Time{})
	workflow.updateRenderStep("render_subtitle", 100, "ASS ready", time.Time{})
	workflow.updateRenderStep("render_encode", 50, "Encoding 00:01:00 / 00:02:00.", eta)

	state := workflowSnapshot(workflow)
	if len(state.RenderSteps) != 4 {
		t.Fatalf("render step count = %d, want 4", len(state.RenderSteps))
	}
	byID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.RenderSteps {
		byID[step.ID] = step
	}
	if got := byID["render_encode"]; got.State != "running" || got.Percent != 50 {
		t.Fatalf("encode phase = %#v, want running at 50%%", got)
	}
	if state.ProcessPercent != 55 {
		t.Fatalf("render process percent = %d, want 55", state.ProcessPercent)
	}
	if state.EstimatedCompletionAt == "" {
		t.Fatal("render ETA was not included in workflow snapshot")
	}

	workflow.finishRenderProgress("output verified")
	state = workflowSnapshot(workflow)
	if state.CompletedAt == "" || state.EstimatedCompletionAt != "" {
		t.Fatalf("completed render timestamps = completed=%q eta=%q", state.CompletedAt, state.EstimatedCompletionAt)
	}
}

func TestRecoverStalledDubbingAudioMakesOldJobRetryable(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowDubbingAudioRunning)
	workflow.DubbingSteps = initialDubbingSteps()
	workflow.updateDubbingStep("synthesize", 50, "speech block 11/22 is ready")
	workflow.UpdatedAt = time.Now().Add(-dubbingHeartbeatTimeout - time.Second).UTC().Format(time.RFC3339)

	if !recoverStalledDubbingAudio(workflow, time.Now()) {
		t.Fatal("recoverStalledDubbingAudio() = false, want stale job recovery")
	}
	state := workflowSnapshot(workflow)
	if state.CurrentStage != workflowFailed || workflow.FailedStage != workflowDubbingAudioRunning {
		t.Fatalf("stale job state = %#v", state)
	}
	if !strings.Contains(state.FailureReason, "90 giây") {
		t.Fatalf("stale job reason = %q", state.FailureReason)
	}
	for _, step := range state.DubbingSteps {
		if step.ID == "synthesize" && step.State != "failed" {
			t.Fatalf("synthesize step = %#v, want failed", step)
		}
	}
}

func TestStartWorkflowDubbingAudioAllowsIntentionalRerunDuringAudioReview(t *testing.T) {
	workflow := &subtitleWorkflow{
		TaskID:              "rerun_audio_review",
		TaskBasePath:        filepath.Join("tasks", "rerun_audio_review"),
		CurrentStage:        workflowAwaitDubbingAudio,
		TranslationApproved: true,
	}
	workflow.mu.Lock()
	retrying := workflow.CurrentStage == workflowFailed && workflow.FailedStage == workflowDubbingAudioRunning
	restartingReviewAudio := workflow.CurrentStage == workflowAwaitDubbingAudio
	allowed := (workflow.CurrentStage == workflowTranslationApproved || retrying || restartingReviewAudio) && workflow.TranslationApproved
	workflow.mu.Unlock()
	if !allowed {
		t.Fatal("audio review state must allow an intentional re-synthesis")
	}
}

func TestWorkflowSnapshotAllowsRenderRetryAfterFailedDubbedRender(t *testing.T) {
	workflow := &subtitleWorkflow{
		TaskID:               "retry_render",
		TaskBasePath:         filepath.Join("tasks", "retry_render"),
		CurrentStage:         workflowFailed,
		FailedStage:          workflowRenderRunning,
		TranslationApproved:  true,
		DubbingRequested:     true,
		DubbingVideoApproved: true,
	}
	state := workflowSnapshot(workflow)
	if !state.CanStart["render"] {
		t.Fatal("render must be retryable after a failed final render with approved dubbed video")
	}
}

func TestDeleteWorkflowRemovesOnlyTaskOwnedDirectory(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	taskID := "delete_workflow_test"
	basePath := filepath.Join("tasks", taskID)
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(basePath, "artifact.txt"), []byte("task data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("outside.txt", []byte("must remain"), 0o644); err != nil {
		t.Fatal(err)
	}
	workflow := &subtitleWorkflow{TaskID: taskID, TaskBasePath: basePath}
	workflowSessions.Store(taskID, workflow)
	storage.SubtitleTasks.Store(taskID, &types.SubtitleTask{TaskId: taskID})
	t.Cleanup(func() {
		workflowSessions.Delete(taskID)
		storage.SubtitleTasks.Delete(taskID)
	})

	if err := (Service{}).DeleteWorkflow(taskID); err != nil {
		t.Fatalf("DeleteWorkflow() error = %v", err)
	}
	if _, err := os.Stat(basePath); !os.IsNotExist(err) {
		t.Fatalf("task directory still exists: %v", err)
	}
	if data, err := os.ReadFile("outside.txt"); err != nil || string(data) != "must remain" {
		t.Fatalf("unrelated file changed: %q, err=%v", data, err)
	}
}

func TestVisualOCRTranslationModeReplacesTheSTTProgressPhase(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowTranslationRunning)
	workflow.SourceMethod = sourceMethodVisualOCR
	workflow.TranslationSteps = initialTranslationStepsFor(workflow.SourceMethod)
	workflow.updateTranslationStep("visual_ocr", 45, "frame scan in progress", time.Time{})

	state := workflowSnapshot(workflow)
	byID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.TranslationSteps {
		byID[step.ID] = step
	}
	if _, found := byID["speech_to_text"]; found {
		t.Fatalf("OCR translation state exposed an STT phase: %#v", state.TranslationSteps)
	}
	if got, found := byID["visual_ocr"]; !found || got.State != "running" || got.Percent != 45 {
		t.Fatalf("visual_ocr phase = %#v, want running 45", got)
	}
}

func TestVisualOCRRequestUsesDefaultsAndRejectsAnInvalidRegion(t *testing.T) {
	language, region, interval, _, err := normalizeWorkflowOCRRequest(dto.StartVideoSubtitleTaskReq{SourceMethod: sourceMethodVisualOCR}, sourceMethodVisualOCR)
	if err != nil {
		t.Fatalf("normalizeWorkflowOCRRequest(default) error = %v", err)
	}
	if language != "en" || region.X != 0.10 || region.Y != 0.70 || region.Width != 0.80 || region.Height != 0.20 || interval < 40 {
		t.Fatalf("OCR defaults = language %q region %#v interval %d", language, region, interval)
	}
	_, _, _, _, err = normalizeWorkflowOCRRequest(dto.StartVideoSubtitleTaskReq{
		SourceMethod:    sourceMethodVisualOCR,
		OCRRegionX:      0.80,
		OCRRegionY:      0.70,
		OCRRegionWidth:  0.30,
		OCRRegionHeight: 0.20,
	}, sourceMethodVisualOCR)
	if err == nil {
		t.Fatal("normalizeWorkflowOCRRequest accepted an out-of-frame OCR region")
	}
}

func TestCombinedSourceMethodKeepsBothExtractorsAndUsesOCRForAlignedText(t *testing.T) {
	method, err := validateWorkflowSourceMethod(sourceMethodSpeechToTextAndOCR)
	if err != nil || method != sourceMethodSpeechToTextAndOCR {
		t.Fatalf("combined source method = %q, %v", method, err)
	}
	steps := initialTranslationStepsFor(method)
	seen := map[string]bool{}
	for _, step := range steps {
		seen[step.ID] = true
	}
	if !seen["speech_to_text"] || !seen["visual_ocr"] || !seen["source_srt"] {
		t.Fatalf("combined source steps = %#v", steps)
	}
	if seen["script_prepare"] || seen["download_video"] || seen["download_audio"] {
		t.Fatalf("stage 02 exposed stage 01 media preparation: %#v", steps)
	}
	directory := t.TempDir()
	sttPath := filepath.Join(directory, "stt.srt")
	ocrPath := filepath.Join(directory, "ocr.srt")
	if err := os.WriteFile(sttPath, []byte("1\n00:00:00,000 --> 00:00:02,000\nheard words\n\n2\n00:00:02,000 --> 00:00:04,000\nsecond line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ocrPath, []byte("1\n00:00:00,500 --> 00:00:01,900\nvisible caption\n"), 0644); err != nil {
		t.Fatal(err)
	}
	replaced, merged, err := combineSTTAndOCRSourceSRT(sttPath, ocrPath)
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 1 || len(merged) != 2 || merged[0].OriginLanguageSentence != "visible caption" || merged[1].OriginLanguageSentence != "second line" {
		t.Fatalf("combined result = replaced %d, blocks %#v", replaced, merged)
	}
}

func TestCombinedSourceExtractorsStartInParallel(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	result := make(chan [2]error, 1)
	go func() {
		sttErr, ocrErr := runParallelSourceExtractors(func() error {
			started <- "stt"
			<-release
			return nil
		}, func() error {
			started <- "ocr"
			<-release
			return nil
		})
		result <- [2]error{sttErr, ocrErr}
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case branch := <-started:
			seen[branch] = true
		case <-time.After(time.Second):
			t.Fatalf("extractor branches did not start together: %#v", seen)
		}
	}
	close(release)
	select {
	case errors := <-result:
		if errors[0] != nil || errors[1] != nil {
			t.Fatalf("parallel extractor errors = %v", errors)
		}
	case <-time.After(time.Second):
		t.Fatal("parallel extractor branches did not finish")
	}
}

func TestWorkflowSourceStepNeverRequestsPlatformCaptions(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowSourceRunning)
	if workflow.stepParam(workflow.task()).VttSwitch {
		t.Fatal("KOVA source workflow must use speech-to-text, not a platform VTT")
	}
}

func TestWorkflowSeparatesAudioAndVideoApprovalGates(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowTranslationApproved)
	workflow.SourceApproved = true
	workflow.TranslationApproved = true

	state := workflowSnapshot(workflow)
	if !state.CanStart["dubbing_audio"] || state.CanStart["dubbing_video"] || !state.CanStart["render"] {
		t.Fatalf("translation-approved gates = %#v, want audio start plus the explicit subtitle-only render branch", state.CanStart)
	}

	workflow.DubbingRequested = true
	workflow.DubbingAudioApproved = true
	workflow.CurrentStage = workflowDubbingAudioApproved
	state = workflowSnapshot(workflow)
	if state.CanStart["dubbing_audio"] || !state.CanStart["dubbing_video"] || state.CanStart["render"] {
		t.Fatalf("audio-approved gates = %#v, want only video mux start", state.CanStart)
	}

	workflow.DubbingVideoApproved = true
	workflow.CurrentStage = workflowDubbingVideoApproved
	state = workflowSnapshot(workflow)
	if !state.CanStart["render"] || state.CanStart["dubbing_video"] {
		t.Fatalf("video-approved gates = %#v, want render only", state.CanStart)
	}
}

func TestApproveWorkflowAudioThenVideoRequiresSeparateArtifacts(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitDubbingAudio)
	workflow.SourceApproved = true
	workflow.TranslationApproved = true
	workflow.DubbingRequested = true
	writeWorkflowFixture(t, workflow, types.TtsResultAudioFileName, "approved audio")

	state, err := (Service{}).ApproveWorkflowStage(workflow.TaskID, "dubbing_audio")
	if err != nil {
		t.Fatalf("approve audio: %v", err)
	}
	if state.CurrentStage != workflowDubbingAudioApproved || !state.CanStart["dubbing_video"] || state.CanStart["render"] {
		t.Fatalf("audio approval state = %#v", state)
	}

	workflow.CurrentStage = workflowAwaitDubbingVideo
	writeWorkflowFixture(t, workflow, types.SubtitleTaskVideoWithTtsFileName, "approved video")
	state, err = (Service{}).ApproveWorkflowStage(workflow.TaskID, "dubbing_video")
	if err != nil {
		t.Fatalf("approve video: %v", err)
	}
	if state.CurrentStage != workflowDubbingVideoApproved || !state.CanStart["render"] {
		t.Fatalf("video approval state = %#v", state)
	}
}

func TestLegacyCombinedDubbingReviewNeverBypassesNewGates(t *testing.T) {
	workflow := &subtitleWorkflow{
		CurrentStage:     "dubbing_approved",
		DubbingRequested: true,
		DubbingApproved:  true,
	}
	if !normalizeLegacyWorkflowDubbingState(workflow) {
		t.Fatal("legacy state was not normalized")
	}
	if workflow.CurrentStage != workflowAwaitDubbingAudio || workflow.DubbingAudioApproved || workflow.DubbingVideoApproved || workflow.DubbingApproved {
		t.Fatalf("legacy normalization bypassed an approval: %#v", workflow)
	}
}

func TestSkipWorkflowDubbingPreservesReviewedTranslationAndPersistsChoice(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowFailed)
	workflow.SourceApproved = true
	workflow.TranslationApproved = true
	workflow.DubbingRequested = true
	workflow.DubbingAudioApproved = false
	workflow.DubbingVideoApproved = false
	workflow.DubbingApproved = false
	workflow.FailedStage = workflowDubbingRunning
	workflow.FailureReason = "remote worker unavailable"

	target := writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, `1
00:00:00,000 --> 00:00:01,000
Ban dich da duyet.
`)
	writeWorkflowFixture(t, workflow, types.SubtitleTaskBilingualSrtFileName, validReviewSRT)
	writeWorkflowFixture(t, workflow, types.TtsResultAudioFileName, "partial dubbed audio")
	writeWorkflowFixture(t, workflow, types.SubtitleTaskVideoWithTtsFileName, "partial dubbed video")
	writeWorkflowFixture(t, workflow, types.SubtitleTaskTransferredVerticalVideoFileName, "temporary vertical video")
	writeWorkflowFixture(t, workflow, filepath.Join("output", types.SubtitleTaskHorizontalEmbedVideoFileName), "rendered horizontal")
	writeWorkflowFixture(t, workflow, filepath.Join("output", types.SubtitleTaskVerticalEmbedVideoFileName), "rendered vertical")
	writeWorkflowFixture(t, workflow, filepath.Join("dubbing", "dub.srt"), validReviewSRT)
	writeWorkflowFixture(t, workflow, filepath.Join(dubbing.DubbingDirName, dubbing.DubbingReportName), `{}`)

	state, err := (Service{}).SkipWorkflowDubbing(workflow.TaskID)
	if err != nil {
		t.Fatalf("SkipWorkflowDubbing(): %v", err)
	}
	if state.CurrentStage != workflowTranslationApproved {
		t.Fatalf("stage = %q, want %q", state.CurrentStage, workflowTranslationApproved)
	}
	if !state.CanStart["render"] || !state.CanStart["dubbing"] || !state.CanStart["dubbing_skip"] {
		t.Fatalf("subtitle-only branch did not enable next actions: %#v", state.CanStart)
	}
	for _, artifact := range state.Artifacts {
		switch artifact.Kind {
		case "dubbed_audio", "dubbed_video", "dubbing_srt", "dubbing_report", "subtitled_horizontal_video", "subtitled_vertical_video":
			t.Fatalf("stale dubbing/render artifact still exposed after skip: %#v", artifact)
		}
	}

	translated, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read reviewed translation after skip: %v", err)
	}
	if !strings.Contains(string(translated), "Ban dich da duyet.") {
		t.Fatalf("skip changed reviewed translated SRT: %q", translated)
	}
	for _, path := range []string{
		filepath.Join(workflow.TaskBasePath, types.TtsResultAudioFileName),
		filepath.Join(workflow.TaskBasePath, types.SubtitleTaskVideoWithTtsFileName),
		filepath.Join(workflow.TaskBasePath, types.SubtitleTaskTransferredVerticalVideoFileName),
		filepath.Join(workflow.TaskBasePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(workflow.TaskBasePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName),
		filepath.Join(workflow.TaskBasePath, "dubbing"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale dubbing output %q remains after skip: %v", path, err)
		}
	}

	persistedJSON, err := os.ReadFile(workflowPath(workflow.TaskBasePath))
	if err != nil {
		t.Fatalf("read persisted workflow state: %v", err)
	}
	var persisted subtitleWorkflow
	if err := json.Unmarshal(persistedJSON, &persisted); err != nil {
		t.Fatalf("decode persisted workflow state: %v", err)
	}
	if persisted.CurrentStage != workflowTranslationApproved || persisted.DubbingRequested || persisted.DubbingAudioApproved || persisted.DubbingVideoApproved || persisted.DubbingApproved || persisted.FailureReason != "" {
		t.Fatalf("persisted skip state = %#v", persisted)
	}
	task := workflow.task()
	if task.Status != types.SubtitleTaskStatusProcessing || task.FailReason != "" {
		t.Fatalf("task did not recover from failed dubbing: %#v", task)
	}
}

func TestSkipWorkflowDubbingRequiresApprovedTranslation(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitTranslation)
	workflow.SourceApproved = true
	if _, err := (Service{}).SkipWorkflowDubbing(workflow.TaskID); err == nil {
		t.Fatal("SkipWorkflowDubbing accepted an unapproved translation")
	}
}

func TestCloneTranslationWarningsNormalizesNilSuspiciousWords(t *testing.T) {
	cloned := cloneTranslationWarnings([]dto.TranslationWarning{{CueIndex: 7}})
	if len(cloned) != 1 {
		t.Fatalf("warning count = %d, want 1", len(cloned))
	}
	if cloned[0].SuspiciousWords == nil {
		t.Fatal("SuspiciousWords must be an empty array, not nil/JSON null")
	}
}

func TestRepairWorkflowTranslationForApprovalNormalizesMalformedModelOutput(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitTranslation)
	workflow.SourceApproved = true
	source := `1
00:00:00,000 --> 00:00:02,000
Source one.

2
00:00:02,000 --> 00:00:04,000
Source two.
`
	malformedTarget := `1
00:00:00,000 --> 00:00:02,000
1
Bản dịch một.
%!(EXTRA string=Source one.)

2
00:00:02,000 --> 00:00:04,000
[[KOVA_TRANSLATION_MISSING]]
`
	writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, source)
	targetPath := writeWorkflowFixture(t, workflow, types.SubtitleTaskTargetLanguageSrtFileName, malformedTarget)

	fallbackCount, err := repairWorkflowTranslationForApproval(workflow)
	if err != nil {
		t.Fatalf("repairWorkflowTranslationForApproval() error = %v", err)
	}
	if fallbackCount != 1 {
		t.Fatalf("fallback count = %d, want 1", fallbackCount)
	}
	cues, err := dubbing.ParseSRTFile(targetPath)
	if err != nil {
		t.Fatalf("repaired target SRT is invalid: %v", err)
	}
	if len(cues) != 2 || cues[0].Text != "Bản dịch một." || cues[1].Text != "Source two." {
		t.Fatalf("repaired cues = %#v", cues)
	}
	if _, err := os.Stat(targetPath + ".before_approval_repair.srt"); err != nil {
		t.Fatalf("original malformed target was not retained: %v", err)
	}
}

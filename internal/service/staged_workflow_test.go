package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestWorkflowRequiresSourceApprovalBeforeTranslation(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowAwaitSourceReview)
	writeWorkflowFixture(t, workflow, types.SubtitleTaskOriginLanguageSrtFileName, validReviewSRT)

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

func TestWorkflowSnapshotKeepsSeparateSourcePhases(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowSourceRunning)
	workflow.updateSourceStep("download_audio", 100, "audio ready")
	workflow.updateSourceStep("download_video", 100, "video ready")
	workflow.updateSourceStep("speech_to_text", 45, "segment 2/4")

	state := workflowSnapshot(workflow)
	if len(state.SourceSteps) != 4 {
		t.Fatalf("source step count = %d, want 4", len(state.SourceSteps))
	}
	byID := map[string]struct {
		state   string
		percent uint8
	}{}
	for _, step := range state.SourceSteps {
		byID[step.ID] = struct {
			state   string
			percent uint8
		}{step.State, step.Percent}
	}
	if got := byID["download_audio"]; got.state != "completed" || got.percent != 100 {
		t.Fatalf("audio phase = %#v, want completed 100", got)
	}
	if got := byID["download_video"]; got.state != "completed" || got.percent != 100 {
		t.Fatalf("video phase = %#v, want completed 100", got)
	}
	if got := byID["speech_to_text"]; got.state != "running" || got.percent != 45 {
		t.Fatalf("STT phase = %#v, want running 45", got)
	}

	workflow.failActiveSourceStep("gateway configuration failed")
	state = workflowSnapshot(workflow)
	for _, step := range state.SourceSteps {
		if step.ID == "speech_to_text" && step.State != "failed" {
			t.Fatalf("STT phase after failure = %q, want failed", step.State)
		}
	}
}

func TestVisualOCRSourceModeReplacesTheSTTProgressPhase(t *testing.T) {
	workflow := seedWorkflowForTest(t, workflowSourceRunning)
	workflow.SourceMethod = sourceMethodVisualOCR
	workflow.SourceSteps = initialSourceStepsFor(workflow.SourceMethod)
	workflow.updateSourceStep("download_video", 100, "video ready")
	workflow.updateSourceStep("visual_ocr", 45, "frame scan in progress")

	state := workflowSnapshot(workflow)
	byID := map[string]dto.WorkflowProgressStep{}
	for _, step := range state.SourceSteps {
		byID[step.ID] = step
	}
	if _, found := byID["speech_to_text"]; found {
		t.Fatalf("OCR source state exposed an STT phase: %#v", state.SourceSteps)
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

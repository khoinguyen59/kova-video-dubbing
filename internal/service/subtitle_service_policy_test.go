package service

import (
	"strings"
	"testing"

	"kova/config"
	"kova/internal/dto"
	"kova/internal/storage"
	"kova/internal/types"
)

func TestStartSubtitleTaskRejectsUnapprovedOmniVoiceCloneBeforeStartingJob(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	config.Conf.Tts.Provider = "omnivoice"

	base := dto.StartVideoSubtitleTaskReq{
		Url:       "https://youtu.be/uDVoZ39mONk",
		Tts:       types.SubtitleTaskTtsYes,
		VttSwitch: true,
	}

	if _, err := (Service{}).StartSubtitleTask(base); err == nil || !strings.Contains(err.Error(), "audio mẫu") {
		t.Fatalf("missing reference error = %v", err)
	}

	base.TtsVoiceCloneSrcFileUrl = "local:C:/does-not-matter-before-consent.wav"
	if _, err := (Service{}).StartSubtitleTask(base); err == nil || !strings.Contains(err.Error(), "xác nhận") {
		t.Fatalf("missing consent error = %v", err)
	}
}

func TestSrtFileToSpeechRejectsOmniVoiceWithoutPerJobConsent(t *testing.T) {
	original := config.Conf
	t.Cleanup(func() { config.Conf = original })
	config.Conf.Tts.Provider = "omnivoice"

	err := (Service{}).srtFileToSpeech(nil, &types.SubtitleTaskStepParam{
		EnableTts:          true,
		TaskBasePath:       t.TempDir(),
		VoiceCloneAudioUrl: "reference.wav",
		VoiceCloneConsent:  false,
	})
	if err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("srtFileToSpeech() error = %v, want consent error", err)
	}
}

func TestGetTaskStatusPreservesOrderedArtifactManifest(t *testing.T) {
	const taskID = "artifact-manifest-status-test"
	storage.SubtitleTasks.Store(taskID, &types.SubtitleTask{
		TaskId: taskID,
		Artifacts: []types.ArtifactInfo{
			{TaskId: taskID, Kind: "source_video", Label: "01 · Video nguồn / Source video", Name: "origin_video.mp4", DownloadUrl: "/api/v1/files/tasks/test/origin_video.mp4"},
			{TaskId: taskID, Kind: "translated_srt", Label: "04 · Phụ đề tiếng Việt / Vietnamese SRT", Name: "target_language_srt.srt", DownloadUrl: "/api/v1/files/tasks/test/target_language_srt.srt"},
			{TaskId: taskID, Kind: "dubbed_audio", Label: "06 · Âm thanh lồng tiếng / Dubbed audio", Name: "tts_final_audio.wav", DownloadUrl: "/api/v1/files/tasks/test/tts_final_audio.wav"},
		},
	})
	t.Cleanup(func() { storage.SubtitleTasks.Delete(taskID) })

	status, err := (Service{}).GetTaskStatus(dto.GetVideoSubtitleTaskReq{TaskId: taskID})
	if err != nil {
		t.Fatalf("GetTaskStatus() error = %v", err)
	}
	if len(status.Artifacts) != 3 {
		t.Fatalf("artifact count = %d, want 3", len(status.Artifacts))
	}
	for index, kind := range []string{"source_video", "translated_srt", "dubbed_audio"} {
		if status.Artifacts[index].Kind != kind {
			t.Errorf("artifact %d kind = %q, want %q", index, status.Artifacts[index].Kind, kind)
		}
		if !strings.HasPrefix(status.Artifacts[index].DownloadUrl, "/api/v1/files/") {
			t.Errorf("artifact %d download URL = %q", index, status.Artifacts[index].DownloadUrl)
		}
	}
}

package service

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"kova/internal/service/dubbing"
	"kova/internal/types"
	"kova/log"
	"kova/pkg/util"
	"os"
	"path/filepath"
	"strings"
)

func artifactDownloadURL(filePath string) string {
	cleaned := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filePath)), "./")
	return "/api/v1/files/" + cleaned
}

func (s Service) uploadSubtitles(ctx context.Context, stepParam *types.SubtitleTaskStepParam) error {
	subtitleInfos := make([]types.SubtitleInfo, 0)
	var err error
	for _, info := range stepParam.SubtitleInfos {
		resultPath := info.Path
		if len(stepParam.ReplaceWordsMap) > 0 { // 需要进行替换
			replacedSrcFile := util.AddSuffixToFileName(resultPath, "_replaced")
			err = util.ReplaceFileContent(resultPath, replacedSrcFile, stepParam.ReplaceWordsMap)
			if err != nil {
				log.GetLogger().Error("uploadSubtitles ReplaceFileContent err", zap.Any("stepParam", stepParam), zap.Error(err))
				return fmt.Errorf("uploadSubtitles ReplaceFileContent err: %w", err)
			}
			resultPath = replacedSrcFile
		}
		subtitleInfos = append(subtitleInfos, types.SubtitleInfo{
			TaskId:      stepParam.TaskId,
			Name:        info.Name,
			DownloadUrl: artifactDownloadURL(resultPath),
		})
	}
	// 更新字幕任务信息
	taskPtr := stepParam.TaskPtr
	taskPtr.SubtitleInfos = subtitleInfos
	taskPtr.Status = types.SubtitleTaskStatusSuccess
	taskPtr.ProcessPct = 100
	// 配音文件
	if stepParam.TtsResultFilePath != "" {
		taskPtr.SpeechDownloadUrl = artifactDownloadURL(stepParam.TtsResultFilePath)
	}
	// Return each actual final MP4 to the desktop. This lets the user select a
	// save location with a native dialog rather than hunting for an internal
	// task directory or typing a path.
	taskPtr.VideoOutputInfos = collectVideoOutputs(stepParam)
	taskPtr.Artifacts = collectArtifacts(stepParam)
	return nil
}

// collectVideoOutputs only publishes artifacts that were actually rendered.
// The three candidate locations cover embedded horizontal/vertical videos and
// the video rebuilt after TTS. A single file can legitimately occupy two of
// those roles, so it is de-duplicated before it reaches the desktop.
func collectVideoOutputs(stepParam *types.SubtitleTaskStepParam) []types.SubtitleInfo {
	videoCandidates := []string{
		filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName),
		filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName),
		stepParam.VideoWithTtsFilePath,
	}
	outputs := make([]types.SubtitleInfo, 0, len(videoCandidates))
	seen := make(map[string]struct{})
	for _, candidate := range videoCandidates {
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			outputs = append(outputs, types.SubtitleInfo{
				TaskId:      stepParam.TaskId,
				Name:        filepath.Base(candidate),
				DownloadUrl: artifactDownloadURL(candidate),
			})
		}
	}
	return outputs
}

// collectArtifacts provides the complete ordered output contract used by the
// Kova desktop. Missing optional artifacts are simply skipped; the order stays
// stable for every finished job.
func collectArtifacts(stepParam *types.SubtitleTaskStepParam) []types.ArtifactInfo {
	type candidate struct {
		kind  string
		label string
		path  string
	}
	candidates := []candidate{
		{"source_video", "01 · Video nguồn / Source video", filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskVideoFileName)},
		{"source_srt", "02 · Phụ đề gốc / Original SRT", filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskOriginLanguageSrtFileName)},
		{"source_text", "02b · Script gốc / Original script", filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskOriginLanguageTextFileName)},
		{"translated_srt", "03 · Phụ đề tiếng Việt / Vietnamese SRT", filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskTargetLanguageSrtFileName)},
		{"dubbed_audio", "04 · Âm thanh lồng tiếng / Dubbed audio", stepParam.TtsResultFilePath},
		{"dubbed_video", "05 · Video đã lắp âm thanh / Video with dubbed audio", stepParam.VideoWithTtsFilePath},
		{"subtitled_horizontal_video", "06 · Video cuối có phụ đề / Final subtitled video", filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskHorizontalEmbedVideoFileName)},
		{"subtitled_vertical_video", "07 · Video cuối dọc có phụ đề / Vertical final video", filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskVerticalEmbedVideoFileName)},
		{"source_audio", "08 · Audio nguồn / Source audio", filepath.Join(stepParam.TaskBasePath, types.SubtitleTaskAudioFileName)},
		{"bilingual_srt", "09 · Phụ đề song ngữ / Bilingual SRT", stepParam.BilingualSrtFilePath},
		{"dubbing_srt", "10 · Phụ đề dùng để lồng tiếng / Dubbing SRT", filepath.Join(stepParam.TaskBasePath, dubbing.DubbingDirName, dubbing.DubSubtitleFileName)},
		{"dubbing_report", "11 · Báo cáo khớp thời lượng / Dubbing timing report", filepath.Join(stepParam.TaskBasePath, dubbing.DubbingDirName, dubbing.DubbingReportName)},
		{"translated_text", "12 · Nội dung đã dịch / Translated text", filepath.Join(stepParam.TaskBasePath, "output", types.SubtitleTaskTargetLanguageTextFileName)},
	}

	artifacts := make([]types.ArtifactInfo, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, item := range candidates {
		if strings.TrimSpace(item.path) == "" {
			continue
		}
		cleaned := filepath.Clean(item.path)
		if _, duplicate := seen[cleaned]; duplicate {
			continue
		}
		info, err := os.Stat(cleaned)
		if err != nil || info.IsDir() {
			continue
		}
		seen[cleaned] = struct{}{}
		artifacts = append(artifacts, types.ArtifactInfo{
			TaskId:      stepParam.TaskId,
			Kind:        item.kind,
			Label:       item.label,
			Name:        filepath.Base(cleaned),
			DownloadUrl: artifactDownloadURL(cleaned),
		})
	}
	return artifacts
}

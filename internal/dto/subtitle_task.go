package dto

type StartVideoSubtitleTaskReq struct {
	AppId                     uint32   `json:"app_id"`
	Url                       string   `json:"url"`
	OriginLanguage            string   `json:"origin_lang"`
	TargetLang                string   `json:"target_lang"`
	Bilingual                 uint8    `json:"bilingual"`
	TranslationSubtitlePos    uint8    `json:"translation_subtitle_pos"`
	ModalFilter               uint8    `json:"modal_filter"`
	Tts                       uint8    `json:"tts"`
	TtsVoiceCode              string   `json:"tts_voice_code"`
	TtsVoiceCloneSrcFileUrl   string   `json:"tts_voice_clone_src_file_url"`
	VoiceCloneConsent         bool     `json:"voice_clone_consent"`
	Replace                   []string `json:"replace"`
	ProtectTerms              []string `json:"protect_terms"`
	Language                  string   `json:"language"`
	EmbedSubtitleVideoType    string   `json:"embed_subtitle_video_type"`
	VerticalMajorTitle        string   `json:"vertical_major_title"`
	VerticalMinorTitle        string   `json:"vertical_minor_title"`
	OriginLanguageWordOneLine int      `json:"origin_language_word_one_line"`
	VttSwitch                 bool     `json:"vtt_switch"` // 是否使用VTT格式字幕文件
}

type StartVideoSubtitleTaskResData struct {
	TaskId string `json:"task_id"`
}

type StartVideoSubtitleTaskRes struct {
	Error int32                          `json:"error"`
	Msg   string                         `json:"msg"`
	Data  *StartVideoSubtitleTaskResData `json:"data"`
}

type GetVideoSubtitleTaskReq struct {
	TaskId string `form:"taskId"`
}

type VideoInfo struct {
	Title                 string `json:"title"`
	Description           string `json:"description"`
	TranslatedTitle       string `json:"translated_title"`
	TranslatedDescription string `json:"translated_description"`
	Language              string `json:"language"`
}

type SubtitleInfo struct {
	Name        string `json:"name"`
	DownloadUrl string `json:"download_url"`
}

type ArtifactInfo struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	DownloadUrl string `json:"download_url"`
}

type GetVideoSubtitleTaskResData struct {
	TaskId            string          `json:"task_id"`
	ProcessPercent    uint8           `json:"process_percent"`
	VideoInfo         *VideoInfo      `json:"video_info"`
	SubtitleInfo      []*SubtitleInfo `json:"subtitle_info"`
	VideoOutputs      []*SubtitleInfo `json:"video_outputs"`
	Artifacts         []*ArtifactInfo `json:"artifacts"`
	TargetLanguage    string          `json:"target_language"`
	SpeechDownloadUrl string          `json:"speech_download_url"`
}

type GetVideoSubtitleTaskRes struct {
	Error int32                        `json:"error"`
	Msg   string                       `json:"msg"`
	Data  *GetVideoSubtitleTaskResData `json:"data"`
}

// UpdateWorkflowSubtitleReq replaces the editable text portion of an SRT
// file. Timestamps and cue order are validated server-side so an edit cannot
// silently desynchronise the render or dubbing stages.
type UpdateWorkflowSubtitleReq struct {
	Content string `json:"content"`
}

// StartWorkflowDubbingReq is intentionally separate from the source request.
// A voice-clone reference and its consent are supplied only when the user
// explicitly starts dubbing; they are never persisted in workflow_state.json.
type StartWorkflowDubbingReq struct {
	TtsVoiceCode            string `json:"tts_voice_code"`
	TtsVoiceCloneSrcFileUrl string `json:"tts_voice_clone_src_file_url"`
	VoiceCloneConsent       bool   `json:"voice_clone_consent"`
}

// WorkflowArtifact is a compact representation used by the staged workflow
// API. It matches the normal job artifact contract while retaining stage
// status and review actions for the desktop UI.
type WorkflowArtifact struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	DownloadUrl string `json:"download_url"`
}

// WorkflowProgressStep exposes independently observable work inside a stage.
// Downloading source media and transcribing it are deliberately separate so a
// completed download remains visible if later speech-to-text fails.
type WorkflowProgressStep struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Percent uint8  `json:"percent"`
	Detail  string `json:"detail,omitempty"`
}

// SubtitleWorkflowData is returned by the staged endpoints. The desktop must
// use CanStart rather than inferring a next operation from progress percent:
// every stage is explicitly user-controlled and may require review first.
type SubtitleWorkflowData struct {
	TaskId            string                 `json:"task_id"`
	SourceUrl         string                 `json:"source_url,omitempty"`
	CurrentStage      string                 `json:"current_stage"`
	ProcessPercent    uint8                  `json:"process_percent"`
	Message           string                 `json:"message"`
	FailureReason     string                 `json:"failure_reason,omitempty"`
	SourceSrtUrl      string                 `json:"source_srt_url,omitempty"`
	TranslatedSrtUrl  string                 `json:"translated_srt_url,omitempty"`
	BilingualSrtUrl   string                 `json:"bilingual_srt_url,omitempty"`
	SourceTextUrl     string                 `json:"source_text_url,omitempty"`
	TranslatedTextUrl string                 `json:"translated_text_url,omitempty"`
	SourceSteps       []WorkflowProgressStep `json:"source_steps,omitempty"`
	Artifacts         []WorkflowArtifact     `json:"artifacts"`
	CanStart          map[string]bool        `json:"can_start"`
	ReviewRequired    bool                   `json:"review_required"`
}

type SubtitleWorkflowRes struct {
	Error int32                 `json:"error"`
	Msg   string                `json:"msg"`
	Data  *SubtitleWorkflowData `json:"data"`
}

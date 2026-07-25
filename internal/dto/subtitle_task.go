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
	// SourceMethod chooses the explicit source-script branch. "speech_to_text"
	// transcribes audio; "visual_ocr" reads visible/hardcoded captions from
	// video frames; "speech_to_text_and_visual_ocr" uses STT as the timed
	// backbone and lets aligned OCR text correct the visible-caption cues.
	SourceMethod string `json:"source_method"`
	// ReviewMode is "manual" by default. In "auto" mode KOVA keeps every
	// artifact but automatically accepts each completed review gate, so the
	// next explicit stage can be started without an approval click.
	ReviewMode string `json:"review_mode"`
	// SourceCookieBrowser controls the isolated temporary browser KOVA may use
	// for short-video platforms which reject anonymous extraction. "auto" tries
	// the public request first and then a brand-new Edge/Chrome profile. Cookie
	// values are never sent through this request or persisted by KOVA.
	SourceCookieBrowser string `json:"source_cookie_browser"`
	// OCREngine selects the OCR runtime. "colab" keeps PaddleOCR and its GPU
	// dependencies out of the desktop; "local" remains an explicit fallback.
	OCREngine           string  `json:"ocr_engine"`
	OCRWorkerURL        string  `json:"ocr_worker_url"`
	OCRWorkerToken      string  `json:"ocr_worker_token"`
	OCRLanguage         string  `json:"ocr_language"`
	OCRRegionX          float64 `json:"ocr_region_x"`
	OCRRegionY          float64 `json:"ocr_region_y"`
	OCRRegionWidth      float64 `json:"ocr_region_width"`
	OCRRegionHeight     float64 `json:"ocr_region_height"`
	OCRSampleIntervalMS int     `json:"ocr_sample_interval_ms"`
	OCRPreferGPU        bool    `json:"ocr_prefer_gpu"`
	// OCRFallbackToSTT applies only to the hybrid source method in Auto mode.
	// When the optional local PaddleOCR runtime is unavailable, KOVA preserves
	// the complete automatic pipeline by continuing with timed STT instead of
	// failing before source media is downloaded.
	OCRFallbackToSTT bool `json:"ocr_fallback_to_stt"`
	VttSwitch        bool `json:"vtt_switch"` // 是否使用VTT格式字幕文件
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

// StartWorkflowRenderReq controls the final burned-in subtitle render. The
// desktop always renders the approved SRT; the optional blur covers the old
// hardcoded caption band before the new ASS subtitles are drawn on top.
type StartWorkflowRenderReq struct {
	BlurOriginalText bool    `json:"blur_original_text"`
	BlurRegionX      float64 `json:"blur_region_x"`
	BlurRegionY      float64 `json:"blur_region_y"`
	BlurRegionWidth  float64 `json:"blur_region_width"`
	BlurRegionHeight float64 `json:"blur_region_height"`
	BlurStrength     int     `json:"blur_strength"`
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

// TranslationWarning is advisory only. KOVA saves the generated SRT and
// requires the user to review it; a suspected English token must never turn a
// completed translation into a failed job.
type TranslationWarning struct {
	CueIndex        int      `json:"cue_index"`
	SuspiciousWords []string `json:"suspicious_words"`
	// Reason is empty for a normal language-detection warning. It is
	// "model_empty" when the model did not produce a usable translation and
	// KOVA deliberately kept a clearly marked source-text placeholder for the
	// user to edit or explicitly approve.
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text"`
}

// SubtitleWorkflowData is returned by the staged endpoints. The desktop must
// use CanStart rather than inferring a next operation from progress percent:
// every stage is explicitly user-controlled and may require review first.
type SubtitleWorkflowData struct {
	TaskId              string                 `json:"task_id"`
	SourceUrl           string                 `json:"source_url,omitempty"`
	ReviewMode          string                 `json:"review_mode,omitempty"`
	CurrentStage        string                 `json:"current_stage"`
	ProcessPercent      uint8                  `json:"process_percent"`
	Message             string                 `json:"message"`
	FailureReason       string                 `json:"failure_reason,omitempty"`
	SourceWarning       string                 `json:"source_warning,omitempty"`
	SourceSrtUrl        string                 `json:"source_srt_url,omitempty"`
	TranslatedSrtUrl    string                 `json:"translated_srt_url,omitempty"`
	BilingualSrtUrl     string                 `json:"bilingual_srt_url,omitempty"`
	SourceTextUrl       string                 `json:"source_text_url,omitempty"`
	TranslatedTextUrl   string                 `json:"translated_text_url,omitempty"`
	SourceSteps         []WorkflowProgressStep `json:"source_steps,omitempty"`
	TranslationSteps    []WorkflowProgressStep `json:"translation_steps,omitempty"`
	DubbingSteps        []WorkflowProgressStep `json:"dubbing_steps,omitempty"`
	RenderSteps         []WorkflowProgressStep `json:"render_steps,omitempty"`
	TranslationWarnings []TranslationWarning   `json:"translation_warnings,omitempty"`
	UpdatedAt           string                 `json:"updated_at,omitempty"`
	// EstimatedCompletionAt is stage-scoped. Translation derives it from the
	// completed cue count; rendering derives it from FFmpeg's encoded
	// timestamp. CompletedAt is only written after the active stage finishes.
	EstimatedCompletionAt string             `json:"estimated_completion_at,omitempty"`
	CompletedAt           string             `json:"completed_at,omitempty"`
	Artifacts             []WorkflowArtifact `json:"artifacts"`
	CanStart              map[string]bool    `json:"can_start"`
	ReviewRequired        bool               `json:"review_required"`
}

type SubtitleWorkflowRes struct {
	Error int32                 `json:"error"`
	Msg   string                `json:"msg"`
	Data  *SubtitleWorkflowData `json:"data"`
}

package api

// WordReplacement is a per-job source-to-target replacement rule.
type WordReplacement struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// SubtitleTask is the Kova API v1 desktop request contract.
type SubtitleTask struct {
	URL                     string   `json:"url"`
	Language                string   `json:"language"`
	OriginLang              string   `json:"origin_lang"`
	TargetLang              string   `json:"target_lang"`
	Bilingual               int      `json:"bilingual"`
	TranslationSubtitlePos  int      `json:"translation_subtitle_pos"`
	TTS                     int      `json:"tts"`
	TTSVoiceCode            string   `json:"tts_voice_code,omitempty"`
	TTSVoiceCloneSrcFileURL string   `json:"tts_voice_clone_src_file_url,omitempty"`
	VoiceCloneConsent        bool     `json:"voice_clone_consent"`
	VttSwitch                bool     `json:"vtt_switch"`
	ModalFilter             int      `json:"modal_filter"`
	Replace                 []string `json:"replace,omitempty"`
	ProtectTerms            []string `json:"protect_terms,omitempty"`
	EmbedSubtitleVideoType  string   `json:"embed_subtitle_video_type"`
	VerticalMajorTitle      string   `json:"vertical_major_title,omitempty"`
	VerticalMinorTitle      string   `json:"vertical_minor_title,omitempty"`
}

// SubtitleResult identifies one downloadable artifact.
type SubtitleResult struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}

// ArtifactResult is returned in the exact order Kova produced the job's
// deliverables, so the desktop can show a complete output checklist.
type ArtifactResult struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}

// TaskStatus is the Kova API v1 job-status contract.
type TaskStatus struct {
	TaskId            string           `json:"task_id"`
	ProcessPercent    int              `json:"process_percent"`
	Status            string           `json:"status"`
	Message           string           `json:"message"`
	SubtitleInfo      []SubtitleResult `json:"subtitle_info"`
	Artifacts         []ArtifactResult `json:"artifacts"`
	SpeechDownloadURL string           `json:"speech_download_url"`
}

package types

import subtitlestyle "kova/internal/subtitle_style"

var SplitTextPrompt = `You are Kova's professional subtitle translator.

Translate the input into %s and split it into short, natural subtitle units.

Rules:
1. Preserve the complete meaning, tone, order, numbers, and named entities.
2. The translated line must be entirely in the target language. Do not leave source-language words unless they are proper names, product names, abbreviations, or protected tokens.
3. For Vietnamese output, use natural contemporary Vietnamese, translate common English technical terms when an established Vietnamese form exists, and never mix English filler text into the result.
4. Split only at punctuation or natural clause boundaries. Do not create one-word fragments.
5. Keep the original text exactly as supplied in the original line.
6. Return only the numbered blocks below; no explanations.

Output format:
1
[translated sentence 1]
[original sentence 1]

2
[translated sentence 2]
[original sentence 2]

If there is no translatable text, output:
[NO_TEXT]

Input:

`

var SplitTextPromptWithModalFilter = `You are Kova's professional subtitle translator.

Translate the input into %s and split it into short, natural subtitle units.
Remove standalone filler sounds such as "um", "uh", "ah", "oh", and repeated stutters.

Rules:
1. Preserve the complete meaning, tone, order, numbers, and named entities.
2. The translated line must be entirely in the target language. Keep only proper names, product names, abbreviations, and protected tokens from the source language.
3. For Vietnamese output, use natural contemporary Vietnamese and do not mix English filler text into the result.
4. Split only at punctuation or natural clause boundaries. Do not create one-word fragments.
5. Keep each original line exactly as supplied, except removed filler-only fragments.
6. Return only the numbered blocks below; no explanations.

Output format:
1
[translated sentence 1]
[original sentence 1]

2
[translated sentence 2]
[original sentence 2]

If there is no translatable text, output:
[NO_TEXT]

Input:

`

var SplitTextPromptJson = `You are Kova's subtitle translation engine.
Translate the input into %s and return a JSON array only.

Requirements:
1. Each item contains "original_sentence" and "translated_sentence".
2. Preserve every original character in "original_sentence".
3. Use only the target language in "translated_sentence", except proper names, abbreviations, product names, and protected tokens.
4. Split at natural boundaries into concise, complete subtitle units.
5. Preserve meaning, tone, numbers, and ordering. Do not add explanations.

Input:

`

var SplitTextPromptWithModalFilterJson = `You are Kova's subtitle translation engine.
Translate the input into %s and return a JSON array only.

Requirements:
1. Each item contains "original_sentence" and "translated_sentence".
2. Remove standalone filler sounds and repeated stutters.
3. Preserve every remaining original character in "original_sentence".
4. Use only the target language in "translated_sentence", except proper names, abbreviations, product names, and protected tokens.
5. Split at natural boundaries into concise, complete subtitle units.
6. Preserve meaning, tone, numbers, and ordering. Do not add explanations.

Input:

`

var TranslateVideoTitleAndDescriptionPrompt = `You are Kova's metadata translator.
Translate the title and description below into %s.
Keep #### as the exact separator between title and description.
Use only the target language except for proper names, product names, abbreviations, URLs, and protected tokens.
Return only the translated title, ####, and translated description.

Source:
%s
`

var SplitLongSentencePrompt = `Split this aligned subtitle pair into short, natural parts.

Original: %s
Translation: %s

Requirements:
1. Concatenated origin_part values must reproduce the original without omissions.
2. Each translated_part must be fluent, complete, and aligned with its origin_part.
3. Restore missing meaning in the translation when necessary, using only the target language except protected names/tokens.
4. Return JSON only:
{"align":[{"origin_part":"part 1","translated_part":"part 1"},{"origin_part":"part 2","translated_part":"part 2"}]}`

var SplitOriginLongSentencePrompt = `Please split the following text into multiple parts, ensuring it's divided into at most 3 short sentences, preferably 2 parts,

Original text: %s

CRITICAL Requirements:
1. The split sentences must exactly match the original text, absolutely no changes to the original text are allowed
2. Split based on sentence meaning, dividing into at most 3 parts, preferably 2 parts
3. **Each split part MUST contain at least 3-5 words. NEVER create single-word or two-word fragments**
4. Split at natural break points (conjunctions, clauses) - DO NOT split phrases like "we're marking out", "you're going to", etc.
5. Try to make the split as balanced as possible while maintaining sentence integrity
6. Return in JSON format only, no other descriptions or explanations
7. Example format:
{"short_sentences":[{"text": "split sentence 1 with at least 3 words"},{"text": "split sentence 2 with at least 3 words"}]}

`

var SplitLongTextByMeaningPrompt = `Please split the following long text into shorter sentences based on semantic meaning. Do not change, add, or remove any words from the original text.

Original text: %s

Requirements:
1. Split the text into as many shorter, meaningful sentences as possible while preserving ALL original words
2. Do NOT change, modify, add, or remove any words - only split at natural breakpoints
3. Split at natural linguistic boundaries such as:
   - Punctuation marks (commas, semicolons, periods)
   - Conjunctions (and, but, or, so, because, when, while, etc.)
   - Relative pronouns (which, that, who, where, etc.)
   - Natural pause points that maintain sentence meaning
4. Each split part should be a complete, meaningful unit that can stand alone
5. Prioritize shorter segments - split as much as possible while maintaining semantic integrity
6. No limit on the number of splits - make each part as short as possible while still being meaningful
7. Maintain the original word order and exact spelling
8. Preserve all original punctuation and capitalization
9. Return in JSON format only, no other descriptions or explanations
10. Example format:
{"short_sentences":[{"text": "first short part"},{"text": "second short part"},{"text": "third short part"}]}

`

var SplitTextWithContextPrompt = `You are Kova's professional subtitle translator.

Target language: %s

Critical rules:
1. Output exactly one line containing only the translation.
2. The output must be entirely in the target language. Keep source-language text only when it is a proper name, product name, abbreviation, URL, or protected token.
3. When the target is Vietnamese, use fluent contemporary Vietnamese; translate common English words and technical terms when a natural Vietnamese equivalent exists.
4. Preserve the speaker's meaning, tone, factual details, names, numbers, and intent.
5. Remove filler sounds and repeated stutters, but never remove meaningful content.
6. Use punctuation native to the target language.
7. Use previous and next sentences only to resolve context; translate only the target sentence.
8. Do not explain, label, quote, transliterate, or include the source sentence.

**Context**:
[Previous Sentences]
%s

[Target Sentence]
%s

[Next Sentences]
%s

Translation only:`

type SmallAudio struct {
	AudioFile         string
	TranscriptionData *TranscriptionData
	SrtNoTsFile       string
}

type SubtitleResultType int

const (
	SubtitleResultTypeOriginOnly                   SubtitleResultType = iota + 1 // 仅返回原语言字幕
	SubtitleResultTypeTargetOnly                                                 // 仅返回翻译后语言字幕
	SubtitleResultTypeBilingualTranslationOnTop                                  // 返回双语字幕，翻译后的字幕在上
	SubtitleResultTypeBilingualTranslationOnBottom                               // 返回双语字幕，翻译后的字幕在下
)

const (
	SubtitleTaskBilingualYes uint8 = iota + 1
	SubtitleTaskBilingualNo
)

const (
	SubtitleTaskTranslationSubtitlePosTop uint8 = iota + 1
	SubtitleTaskTranslationSubtitlePosBelow
)

const (
	SubtitleTaskModalFilterYes uint8 = iota + 1
	SubtitleTaskModalFilterNo
)

const (
	SubtitleTaskTtsYes uint8 = iota + 1
	SubtitleTaskTtsNo
)

const (
	SubtitleTaskTtsVoiceCodeLongyu uint8 = iota + 1
	SubtitleTaskTtsVoiceCodeLongchen
)

const (
	SubtitleTaskStatusProcessing uint8 = iota + 1
	SubtitleTaskStatusSuccess
	SubtitleTaskStatusFailed
)

const (
	SubtitleTaskAudioFileName                                    = "origin_audio.mp3"
	SubtitleTaskVideoFileName                                    = "origin_video.mp4"
	SubtitleTaskSplitAudioFileNamePrefix                         = "split_audio"
	SubtitleTaskSplitAudioFileNamePattern                        = SubtitleTaskSplitAudioFileNamePrefix + "_%03d.mp3"
	SubtitleTaskSplitAudioTxtFileNamePattern                     = "split_audio_txt_%d.txt"
	SubtitleTaskSplitAudioWordsFileNamePattern                   = "split_audio_words_%d.txt"
	SubtitleTaskSplitSrtNoTimestampFileNamePattern               = "srt_no_ts_%d.srt"
	SubtitleTaskSrtNoTimestampFileName                           = "srt_no_ts.srt"
	SubtitleTaskSplitBilingualSrtFileNamePattern                 = "split_bilingual_srt_%d.srt"
	SubtitleTaskSplitShortOriginMixedSrtFileNamePattern          = "split_short_origin_mixed_srt_%d.srt" //长中文+短英文
	SubtitleTaskSplitShortOriginSrtFileNamePattern               = "split_short_origin_srt_%d.srt"       //短英文
	SubtitleTaskBilingualSrtFileName                             = "bilingual_srt.srt"
	SubtitleTaskShortOriginMixedSrtFileName                      = "short_origin_mixed_srt.srt" //长中文+短英文
	SubtitleTaskShortOriginSrtFileName                           = "short_origin_srt.srt"       //短英文
	SubtitleTaskOriginLanguageSrtFileName                        = "origin_language_srt.srt"
	SubtitleTaskOriginLanguageTextFileName                       = "origin_language.txt"
	SubtitleTaskTargetLanguageSrtFileName                        = "target_language_srt.srt"
	SubtitleTaskTargetLanguageTextFileName                       = "target_language.txt"
	SubtitleTaskStepParamGobPersistenceFileName                  = "step_param.gob"
	SubtitleTaskAudioTranscriptionDataPersistenceFileNamePattern = "audio_transcription_data_%d.json"
	SubtitleTaskTranslationRawDataPersistenceFileNamePattern     = "audio_translation_raw_data_%d.json"
	SubtitleTaskTranslationDataPersistenceFileNamePattern        = "translation_data_%d.json"
	SubtitleTaskTransferredVerticalVideoFileName                 = "transferred_vertical_video.mp4"
	SubtitleTaskHorizontalEmbedVideoFileName                     = "horizontal_embed.mp4"
	SubtitleTaskVerticalEmbedVideoFileName                       = "vertical_embed.mp4"
	SubtitleTaskVideoWithTtsFileName                             = "video_with_tts.mp4"
)

const (
	TtsAudioDurationDetailsFileName = "audio_duration_details.txt"
	TtsResultAudioFileName          = "tts_final_audio.wav"
)

const (
	AsrMono16kAudioFileName = "mono_16k_audio.mp3"
)

type SubtitleFileInfo struct {
	Name               string
	Path               string
	LanguageIdentifier string // 在最终下载的文件里标识语言，如zh_cn，en，bilingual
}

type SubtitleTaskStepParam struct {
	TaskId                      string
	TaskPtr                     *SubtitleTask // 和storage里面对应
	TaskBasePath                string
	Link                        string
	AudioFilePath               string
	VttFile                     string // YouTube下载的原始字幕文件路径
	SubtitleResultType          SubtitleResultType
	EnableModalFilter           bool
	EnableTts                   bool
	TtsVoiceCode                string // 人声语音编码
	VoiceCloneAudioUrl          string // 音色克隆的源音频oss地址
	VoiceCloneConsent           bool
	ReplaceWordsMap             map[string]string
	ProtectedTerms              map[string]string    // source term -> stable token while translating
	OriginLanguage              StandardLanguageCode // 视频源语言
	TargetLanguage              StandardLanguageCode // 用户希望的目标翻译语言
	UserUILanguage              StandardLanguageCode // 用户的使用语言
	BilingualSrtFilePath        string
	ShortOriginMixedSrtFilePath string
	SubtitleInfos               []SubtitleFileInfo
	TtsSourceFilePath           string
	TtsResultFilePath           string
	InputVideoPath              string // 源视频路径
	EmbedSubtitleVideoType      string // 合成字幕嵌入的视频类型 none不嵌入 horizontal横屏 vertical竖屏
	VerticalVideoMajorTitle     string // 合成竖屏视频的主标题
	VerticalVideoMinorTitle     string
	MaxWordOneLine              int                     // 字幕一行最多显示多少个字
	VideoWithTtsFilePath        string                  // 替换源视频的音频为tts结果后的视频路径
	VttSwitch                   bool                    // 是否使用VTT格式字幕文件
	SubtitleStyle               *subtitlestyle.StyleSet // CLI/Agent 传入的字幕样式；nil 时使用默认样式
	RenderWidth                 int                     // 当前待烧录字幕视频宽度，用于按字号估算自动换行
	RenderHeight                int                     // 当前待烧录字幕视频高度，用于按字号估算自动换行
	// SourceProgress is used only by KOVA's explicit source workflow. It is
	// in-memory; persistent phase state belongs to the staged workflow.
	SourceProgress func(id string, percent uint8, detail string)
}

type SrtSentence struct {
	Text  string
	Start float64
	End   float64
}

type SrtSentenceWithStrTime struct {
	Text  string
	Start string
	End   string
}

type SubtitleInfo struct {
	Id          uint64 `json:"id" gorm:"column:id"`                                  // 自增id
	TaskId      string `json:"task_id" gorm:"column:task_id"`                        // task_id
	Uid         uint32 `json:"uid" gorm:"column:uid"`                                // 用户id
	Name        string `json:"name" gorm:"column:name"`                              // 字幕名称
	DownloadUrl string `json:"download_url" gorm:"column:download_url"`              // 字幕地址
	CreateTime  int64  `json:"create_time" gorm:"column:create_time;autoCreateTime"` // 创建时间
}

// ArtifactInfo is the ordered, user-facing output manifest for a Kova job.
// Unlike SubtitleInfo, it covers every deliverable, including source media,
// translated subtitles, dubbed audio, and final rendered videos.
type ArtifactInfo struct {
	TaskId      string `json:"task_id" gorm:"column:task_id"`
	Kind        string `json:"kind" gorm:"column:kind"`
	Label       string `json:"label" gorm:"column:label"`
	Name        string `json:"name" gorm:"column:name"`
	DownloadUrl string `json:"download_url" gorm:"column:download_url"`
}

type SubtitleTask struct {
	Id                    uint64         `json:"id" gorm:"column:id"`                                         // 自增id
	TaskId                string         `json:"task_id" gorm:"column:task_id"`                               // 任务id
	Title                 string         `json:"title" gorm:"column:title"`                                   // 标题
	Description           string         `json:"description" gorm:"column:description"`                       // 描述
	TranslatedTitle       string         `json:"translated_title" gorm:"column:translated_title"`             // 翻译后的标题
	TranslatedDescription string         `json:"translated_description" gorm:"column:translated_description"` // 翻译后的描述
	OriginLanguage        string         `json:"origin_language" gorm:"column:origin_language"`               // 视频原语言
	TargetLanguage        string         `json:"target_language" gorm:"column:target_language"`               // 翻译任务的目标语言
	VideoSrc              string         `json:"video_src" gorm:"column:video_src"`                           // 视频地址
	Status                uint8          `json:"status" gorm:"column:status"`                                 // 1-处理中,2-成功,3-失败
	LastSuccessStepNum    uint8          `json:"last_success_step_num" gorm:"column:last_success_step_num"`   // 最后成功的子任务序号，用于任务恢复
	FailReason            string         `json:"fail_reason" gorm:"column:fail_reason"`                       // 失败原因
	ProcessPct            uint8          `json:"process_percent" gorm:"column:process_percent"`               // 处理进度
	Duration              uint32         `json:"duration" gorm:"column:duration"`                             // 视频时长
	SrtNum                int            `json:"srt_num" gorm:"column:srt_num"`                               // 字幕数量
	SubtitleInfos         []SubtitleInfo `gorm:"foreignKey:TaskId;references:TaskId"`
	VideoOutputInfos      []SubtitleInfo `gorm:"-"`
	Artifacts             []ArtifactInfo `gorm:"-"`
	Cover                 string         `json:"cover" gorm:"column:cover"`                             // 封面
	SpeechDownloadUrl     string         `json:"speech_download_url" gorm:"column:speech_download_url"` // 语音文件下载地址
	CreateTime            int64          `json:"create_time" gorm:"column:create_time;autoCreateTime"`  // 创建时间
	UpdateTime            int64          `json:"update_time" gorm:"column:update_time;autoUpdateTime"`  // 更新时间
}

type Word struct {
	Num   int
	Text  string
	Start float64
	End   float64
}

type TranscriptionData struct {
	Language string
	Text     string
	Words    []Word
	Segments []TranscriptionSegment
}

// TranscriptionSegment is the timestamped fallback returned by some
// OpenAI-compatible STT providers when word timestamps are unavailable.
// KOVA can still create reviewable source subtitles from these segments.
type TranscriptionSegment struct {
	Text  string
	Start float64
	End   float64
}

type SrtBlock struct {
	Index                  int
	Timestamp              string
	OriginLanguageSentence string
	TargetLanguageSentence string
}

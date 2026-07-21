// Package capcutstudio implements Kova's own media-to-draft planning layer.
//
// It creates an explicit, inspectable CapCut CLI compile specification rather
// than manipulating an opaque CapCut database directly. The generated JSON is
// a Kova artifact: users can inspect it, keep it with the media, and optionally
// compile it with the independently installed capcut-cli executable.
package capcutstudio

import "time"

type Motion string

const (
	MotionZoomIn   Motion = "zoom-in"
	MotionZoomOut  Motion = "zoom-out"
	MotionPanLeft  Motion = "pan-left"
	MotionPanRight Motion = "pan-right"
	MotionNone     Motion = "none"
)

type MaskShape string

const (
	MaskCircle    MaskShape = "circle"
	MaskRectangle MaskShape = "rectangle"
)

// Source accepts exactly one video or an image folder/list. Image files are
// ordered by filename for reproducible scene assignment.
type Source struct {
	VideoPath      string
	ImageDirectory string
	ImagePaths     []string
}

type Watermark struct {
	Path    string
	X       float64 // normalized centre position, -1..1
	Y       float64 // normalized centre position, -1..1
	Scale   float64 // 0.02..2
	Opacity float64 // 0..1
}

type BlurMask struct {
	Shape   MaskShape
	X       float64 // normalized left, 0..1
	Y       float64 // normalized top, 0..1
	Width   float64 // normalized width, 0..1
	Height  float64 // normalized height, 0..1
	Feather float64 // normalized feather, 0..1
	Start   float64
	End     float64 // zero means until the end of the project
}

// TextStyle is stored in a draft spec and maps directly to a CapCut text
// track. The source and translated tracks intentionally have independent
// styles and vertical positions.
type TextStyle struct {
	FontFamily      string  `json:"fontFamily"`
	FontSize        int     `json:"fontSize"`
	Color           string  `json:"color"`
	OutlineColor    string  `json:"outlineColor"`
	OutlineWidth    float64 `json:"outlineWidth"`
	Background      string  `json:"backgroundColor"`
	BackgroundAlpha float64 `json:"backgroundAlpha"`
	ShadowColor     string  `json:"shadowColor"`
	ShadowAlpha     float64 `json:"shadowAlpha"`
	ShadowDistance  float64 `json:"shadowDistance"`
	Alignment       string  `json:"alignment"`
	Bold            bool    `json:"bold"`
	Italic          bool    `json:"italic"`
}

func DefaultSourceStyle() TextStyle {
	return TextStyle{
		FontFamily: "Arial", FontSize: 46, Color: "#FFFFFF", OutlineColor: "#000000",
		OutlineWidth: 3, Background: "#000000", BackgroundAlpha: 0, ShadowColor: "#000000",
		ShadowAlpha: 0.55, ShadowDistance: 2, Alignment: "center", Bold: true,
	}
}

func DefaultTargetStyle() TextStyle {
	style := DefaultSourceStyle()
	style.FontSize = 50
	style.Color = "#FFD54A"
	return style
}

type BuildRequest struct {
	Name                 string
	Source               Source
	VoiceoverInputs      []string // files and/or folders
	BackgroundInputs     []string // files and/or folders
	DefaultImageDuration float64
	VoiceoverVolume      float64
	BackgroundVolume     float64
	DuckingRatio         float64
	Motions              []Motion
	Transition           string
	TransitionDuration   float64
	Watermark            *Watermark
	BlurMasks            []BlurMask

	// SourceSRT and TargetSRT create two separate text tracks in the draft.
	// They are never concatenated into one text item.
	SourceSRT           string
	TargetSRT           string
	SourceLanguage      string
	TargetLanguage      string
	SourceSubtitleStyle TextStyle
	TargetSubtitleStyle TextStyle
	SourceSubtitleY     float64
	TargetSubtitleY     float64

	Width        int
	Height       int
	FPS          int
	RandomSeed   int64 // zero chooses a seed and returns it in the result
	OutputDir    string
	CompileDraft bool
}

type Config struct {
	FFprobePath        string
	NodePath           string
	CapCutCLIPath      string
	CompilerBackend    string // "capcut-cli" or "pycapcut"
	PythonPath         string
	PyCapCutBridgePath string
	CapCutDraftRoot    string
	CompileTimeout     time.Duration
}

type BuildResult struct {
	SpecPath             string
	DraftDirectory       string
	Compiled             bool
	CompilerBackend      string
	CompileLog           string
	TimelineDuration     float64
	SourceMode           string
	ImageCount           int
	VoiceoverCount       int
	BackgroundPath       string
	BackgroundLoops      int
	MotionOperationCount int
	TransitionCount      int
	DuckingKeyframes     int
	WatermarkApplied     bool
	BlurMaskCount        int
	SourceSubtitleCount  int
	TargetSubtitleCount  int
	RandomSeed           int64
}

// DraftSpec is deliberately a compact public contract. capcut-cli reads this
// representation while Kova keeps its extra audit information in metadata.
type DraftSpec struct {
	Name       string           `json:"name"`
	Width      int              `json:"width"`
	Height     int              `json:"height"`
	FPS        int              `json:"fps"`
	Tracks     []DraftTrack     `json:"tracks"`
	Operations []map[string]any `json:"operations"`
	Metadata   map[string]any   `json:"kova_metadata"`
}

type DraftTrack struct {
	Type  string           `json:"type"`
	Name  string           `json:"name"`
	Items []map[string]any `json:"items"`
}

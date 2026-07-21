package service

import (
	"errors"
	"strings"
	"testing"

	"kova/internal/types"
)

func TestSourceSRTBlocksFromTranscriptionCreatesTimedWordCues(t *testing.T) {
	blocks, err := sourceSRTBlocksFromTranscription(&types.TranscriptionData{Words: []types.Word{
		{Text: "Hello", Start: 0, End: 0.4},
		{Text: "from", Start: 0.42, End: 0.7},
		{Text: "KOVA.", Start: 0.72, End: 1.1},
		{Text: "Second", Start: 1.4, End: 1.7},
		{Text: "sentence!", Start: 1.72, End: 2.1},
	}}, 60)
	if err != nil {
		t.Fatalf("sourceSRTBlocksFromTranscription() error = %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("cue count = %d, want 2", len(blocks))
	}
	if blocks[0].Timestamp != "00:01:00,000 --> 00:01:01,099" || blocks[0].OriginLanguageSentence != "Hello from KOVA." {
		t.Fatalf("first cue = %#v", blocks[0])
	}
	if blocks[1].Index != 2 || !strings.Contains(blocks[1].OriginLanguageSentence, "Second") {
		t.Fatalf("second cue = %#v", blocks[1])
	}
}

func TestSourceSRTBlocksFromTranscriptionUsesSegmentsWithoutWords(t *testing.T) {
	blocks, err := sourceSRTBlocksFromTranscription(&types.TranscriptionData{Segments: []types.TranscriptionSegment{
		{Text: "First detected sentence.", Start: 0.1, End: 1.2},
		{Text: "Second detected sentence.", Start: 1.3, End: 2.4},
	}}, 0)
	if err != nil {
		t.Fatalf("sourceSRTBlocksFromTranscription() error = %v", err)
	}
	if len(blocks) != 2 || blocks[1].Timestamp != "00:00:01,299 --> 00:00:02,400" {
		t.Fatalf("segment cues = %#v", blocks)
	}
}

func TestSourceSRTBlocksFromTranscriptionRejectsUntimedResponse(t *testing.T) {
	_, err := sourceSRTBlocksFromTranscription(&types.TranscriptionData{Text: "No timing metadata"}, 0)
	if err == nil || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("untimed response error = %v, want timestamp guidance", err)
	}
}

func TestSourceTranscriptionErrorExplainsGatewayCredentialFailure(t *testing.T) {
	err := sourceTranscriptionError(1, 3, errors.New("status: 400, message: No credentials for provider: openai"))
	if err == nil || !strings.Contains(err.Error(), "API Gateway") || !strings.Contains(err.Error(), "/v1/audio/transcriptions") {
		t.Fatalf("gateway credential error = %v, want actionable guidance", err)
	}
}

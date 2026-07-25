package util

import (
	"fmt"
	"kova/internal/storage"
	"strings"
	"testing"
)

func TestGetAudioDurationRejectsUninitializedFfprobeBeforeExecuting(t *testing.T) {
	original := storage.FfprobePath
	t.Cleanup(func() { storage.FfprobePath = original })
	storage.FfprobePath = ""

	_, err := GetAudioDuration("missing.wav")
	if err == nil || !strings.Contains(err.Error(), "has not been initialized") {
		t.Fatalf("GetAudioDuration() error = %v, want initialization error", err)
	}
	if strings.Contains(err.Error(), "no command") {
		t.Fatalf("GetAudioDuration() exposed raw empty-command failure: %v", err)
	}
}

func TestSplitTextSentences(t *testing.T) {
	origin_sentence := "Now, I'm Ryan D'Aris, founder and CEO of flowstate.com"
	sentences := SplitTextSentences(origin_sentence, 55)
	fmt.Println("origin sentence:", origin_sentence)
	for i, sentence := range sentences {
		fmt.Printf("sentence %d, got '%s'\n", i, sentence)
	}

	// 期望的结果：应该保持为一个完整句子，因为有效字符数小于70
	if len(sentences) != 1 {
		t.Errorf("Expected 1 sentence, got %d sentences", len(sentences))
	}
}

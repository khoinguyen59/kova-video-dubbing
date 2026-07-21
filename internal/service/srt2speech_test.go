package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSrtFileToSpeechRejectsNilStepParam(t *testing.T) {
	err := Service{}.srtFileToSpeech(context.Background(), nil)
	if err == nil {
		t.Fatal("srtFileToSpeech() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "srtFileToSpeech stepParam is nil") {
		t.Fatalf("srtFileToSpeech() error = %q, want nil stepParam error", err)
	}
}

func TestResolveDubbingVoiceCodeUsesCloneCode(t *testing.T) {
	var gotPrefix string
	var gotAudioURL string
	clone := func(prefix, audioURL string) (string, error) {
		gotPrefix = prefix
		gotAudioURL = audioURL
		return "cloned-code", nil
	}

	got, err := resolveDubbingVoiceCode("base", "clone.wav", clone)
	if err != nil {
		t.Fatalf("resolveDubbingVoiceCode() error = %v, want nil", err)
	}
	if got != "cloned-code" {
		t.Fatalf("resolveDubbingVoiceCode() = %q, want %q", got, "cloned-code")
	}
	if gotPrefix != "kova" {
		t.Fatalf("clone prefix = %q, want %q", gotPrefix, "kova")
	}
	if gotAudioURL != "clone.wav" {
		t.Fatalf("clone audioURL = %q, want %q", gotAudioURL, "clone.wav")
	}
}

func TestResolveDubbingVoiceCodeWithoutCloneURLReturnsBaseVoice(t *testing.T) {
	called := false
	clone := func(prefix, audioURL string) (string, error) {
		called = true
		return "cloned-code", nil
	}

	got, err := resolveDubbingVoiceCode("base", "", clone)
	if err != nil {
		t.Fatalf("resolveDubbingVoiceCode() error = %v, want nil", err)
	}
	if got != "base" {
		t.Fatalf("resolveDubbingVoiceCode() = %q, want %q", got, "base")
	}
	if called {
		t.Fatal("clone was called without clone URL")
	}
}

func TestResolveGatewayVoiceCodeUsesPresetAndRejectsCloneAudio(t *testing.T) {
	got, err := resolveGatewayVoiceCode("en-US-JennyNeural", "")
	if err != nil || got != "en-US-JennyNeural" {
		t.Fatalf("resolveGatewayVoiceCode() = %q, %v", got, err)
	}
	if _, err := resolveGatewayVoiceCode("auto", "clone.wav"); err == nil || !strings.Contains(err.Error(), "không hỗ trợ audio clone") {
		t.Fatalf("gateway clone error = %v", err)
	}
}

func TestTargetSRTPathForDubbingUsesTargetLanguageFile(t *testing.T) {
	base := filepath.Join("tasks", "demo")
	got := targetSRTPathForDubbing(base)
	want := filepath.Join(base, "target_language_srt.srt")
	if got != want {
		t.Fatalf("targetSRTPathForDubbing() = %q, want %q", got, want)
	}
}

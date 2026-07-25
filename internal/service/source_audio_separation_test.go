package service

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceWorkflowNeedsAudioSeparationOnlyForSTTMethods(t *testing.T) {
	if !sourceWorkflowNeedsAudioSeparation(sourceMethodSpeechToText) || !sourceWorkflowNeedsAudioSeparation(sourceMethodSpeechToTextAndOCR) {
		t.Fatal("STT source methods must separate vocals before transcription")
	}
	if sourceWorkflowNeedsAudioSeparation(sourceMethodVisualOCR) {
		t.Fatal("OCR-only source does not submit an audio stem to STT")
	}
}

func TestExtractSeparatedStemArchiveRequiresVocalsAndBackground(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "stems.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, body := range map[string]string{"nested/vocals.wav": "clean-voice", "nested/no_vocals.wav": "music-only"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	vocals := filepath.Join(dir, "source_vocals.wav")
	background := filepath.Join(dir, "source_background.wav")
	if err := extractSeparatedStemArchive(archivePath, vocals, background); err != nil {
		t.Fatalf("extractSeparatedStemArchive() error = %v", err)
	}
	if got, _ := os.ReadFile(vocals); string(got) != "clean-voice" {
		t.Fatalf("vocals = %q", got)
	}
	if got, _ := os.ReadFile(background); string(got) != "music-only" {
		t.Fatalf("background = %q", got)
	}
}

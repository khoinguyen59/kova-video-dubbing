package visualocr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRemoteHealthRequiresCUDAAndBearerToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" || request.Header.Get("Authorization") != "Bearer test-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"ready":true,"device":"cuda","engine":"PaddleOCR"}`))
	}))
	defer server.Close()

	health, err := CheckRemoteHealth(context.Background(), RemoteConfig{BaseURL: server.URL, Token: "test-token", Client: server.Client()})
	if err != nil {
		t.Fatalf("CheckRemoteHealth() error = %v", err)
	}
	if !health.Ready || health.Device != "cuda" {
		t.Fatalf("unexpected health = %#v", health)
	}
}

func TestExtractRemoteStreamsVideoAndStoresSRT(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/ocr/extract" || request.Header.Get("Authorization") != "Bearer test-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := request.ParseMultipartForm(32 << 20); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		file, _, err := request.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile() error = %v", err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if string(data) != "fake-video" || request.FormValue("roi") != "0.100000,0.700000,0.800000,0.200000" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"srt":"1\n00:00:00,000 --> 00:00:01,000\nHello\n","device":"cuda","frame_count":4,"cue_count":1,"normalized_cjk":true}`))
	}))
	defer server.Close()

	directory := t.TempDir()
	input := filepath.Join(directory, "source.mp4")
	output := filepath.Join(directory, "out", "source.srt")
	if err := os.WriteFile(input, []byte("fake-video"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := ExtractRemote(context.Background(), RemoteConfig{BaseURL: server.URL, Token: "test-token", Client: server.Client()}, Request{
		VideoPath: input, OutputSRTPath: output, Region: Region{X: .1, Y: .7, Width: .8, Height: .2}, Language: "en", SampleIntervalMS: 250, MergeGapMS: 450,
	})
	if err != nil {
		t.Fatalf("ExtractRemote() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), "Hello") || result.CueCount != 1 {
		t.Fatalf("result=%#v data=%q error=%v", result, data, err)
	}
}

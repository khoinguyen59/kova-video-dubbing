package visualocr

// This file implements the authenticated Colab transport for Visual OCR. The
// OCR implementation still lives in kova_visual_ocr.py, but running it in a
// separate Colab worker keeps Paddle/PaddleOCR and its GPU dependencies out of
// the desktop machine.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RemoteConfig struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

type RemoteHealth struct {
	Ready  bool   `json:"ready"`
	Device string `json:"device"`
	Engine string `json:"engine"`
}

func NormalizeWorkerURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("chưa dán URL worker OCR Google Colab")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", errors.New("URL worker OCR phải là HTTPS, ví dụ https://xxxx.trycloudflare.com")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("URL worker OCR chỉ là địa chỉ gốc, không thêm /v1 hoặc đường dẫn")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func CheckRemoteHealth(parent context.Context, config RemoteConfig) (RemoteHealth, error) {
	baseURL, token, client, err := remoteSettings(config)
	if err != nil {
		return RemoteHealth{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return RemoteHealth{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return RemoteHealth{}, fmt.Errorf("không kết nối được worker OCR Colab: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return RemoteHealth{}, fmt.Errorf("worker OCR Colab trả về %s: %s", response.Status, compactRemoteError(body))
	}
	var health RemoteHealth
	if err := json.Unmarshal(body, &health); err != nil {
		return RemoteHealth{}, errors.New("worker OCR Colab trả về health không hợp lệ")
	}
	if !health.Ready {
		return RemoteHealth{}, errors.New("worker OCR Colab chưa sẵn sàng")
	}
	if !strings.EqualFold(strings.TrimSpace(health.Device), "cuda") {
		return RemoteHealth{}, errors.New("worker OCR Colab chưa chạy CUDA; hãy chọn GPU rồi Run all notebook OCR")
	}
	return health, nil
}

// ExtractRemote streams the already-downloaded source video to the independent
// OCR Colab worker. The returned SRT is saved locally so later review, editing
// and rendering stages never depend on the temporary tunnel.
func ExtractRemote(parent context.Context, config RemoteConfig, value Request) (Result, error) {
	if err := validateRequest(value); err != nil {
		return Result{}, err
	}
	baseURL, token, client, err := remoteSettings(config)
	if err != nil {
		return Result{}, err
	}
	file, err := os.Open(value.VideoPath)
	if err != nil {
		return Result{}, fmt.Errorf("không thể mở video gửi OCR Colab: %w", err)
	}
	defer file.Close()

	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	writeResult := make(chan error, 1)
	go func() {
		defer close(writeResult)
		defer writer.Close()
		defer form.Close()
		if err := form.WriteField("roi", formatRegion(value.Region)); err != nil {
			_ = writer.CloseWithError(err)
			writeResult <- err
			return
		}
		if err := form.WriteField("language", strings.TrimSpace(value.Language)); err != nil {
			_ = writer.CloseWithError(err)
			writeResult <- err
			return
		}
		if err := form.WriteField("interval_ms", strconv.Itoa(value.SampleIntervalMS)); err != nil {
			_ = writer.CloseWithError(err)
			writeResult <- err
			return
		}
		if err := form.WriteField("merge_gap_ms", strconv.Itoa(value.MergeGapMS)); err != nil {
			_ = writer.CloseWithError(err)
			writeResult <- err
			return
		}
		part, err := form.CreateFormFile("file", filepath.Base(value.VideoPath))
		if err == nil {
			_, err = io.Copy(part, file)
		}
		if err != nil {
			_ = writer.CloseWithError(err)
			writeResult <- err
			return
		}
		writeResult <- nil
	}()

	ctx, cancel := context.WithTimeout(parent, 60*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/ocr/extract", reader)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.Do(request)
	if writeErr := <-writeResult; writeErr != nil {
		return Result{}, fmt.Errorf("không thể gửi video đến OCR Colab: %w", writeErr)
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, errors.New("OCR Colab vượt quá 60 phút; hãy kiểm tra tunnel, GPU và video nguồn")
		}
		return Result{}, fmt.Errorf("gọi OCR Colab thất bại: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 12<<20))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("OCR Colab trả về %s: %s", response.Status, compactRemoteError(body))
	}
	var result struct {
		SRT           string `json:"srt"`
		Device        string `json:"device"`
		FrameCount    int    `json:"frame_count"`
		CueCount      int    `json:"cue_count"`
		DroppedFrames int    `json:"dropped_frames"`
		NormalizedCJK bool   `json:"normalized_cjk"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Result{}, errors.New("OCR Colab trả về dữ liệu không hợp lệ")
	}
	if strings.TrimSpace(result.SRT) == "" {
		return Result{}, errors.New("OCR Colab không tạo được nội dung SRT")
	}
	if err := os.MkdirAll(filepath.Dir(value.OutputSRTPath), 0755); err != nil {
		return Result{}, fmt.Errorf("không thể tạo thư mục SRT OCR: %w", err)
	}
	if err := os.WriteFile(value.OutputSRTPath, []byte(result.SRT), 0644); err != nil {
		return Result{}, fmt.Errorf("không thể lưu SRT từ OCR Colab: %w", err)
	}
	return Result{
		SRTPath:       value.OutputSRTPath,
		Device:        firstNonEmptyText(result.Device, "cuda"),
		FrameCount:    result.FrameCount,
		CueCount:      result.CueCount,
		DroppedFrames: result.DroppedFrames,
		NormalizedCJK: result.NormalizedCJK,
	}, nil
}

func remoteSettings(config RemoteConfig) (string, string, *http.Client, error) {
	baseURL, err := NormalizeWorkerURL(config.BaseURL)
	if err != nil {
		return "", "", nil, err
	}
	token := strings.TrimSpace(config.Token)
	if token == "" {
		return "", "", nil, errors.New("chưa dán token OCR Google Colab")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{}
	}
	return baseURL, token, client, nil
}

func formatRegion(region Region) string {
	return fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", region.X, region.Y, region.Width, region.Height)
}

func compactRemoteError(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 1000 {
		return text[:1000] + "…"
	}
	if text == "" {
		return "không có nội dung lỗi"
	}
	return text
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

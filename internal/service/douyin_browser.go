package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kova/internal/processutil"
	"kova/internal/storage"

	"github.com/gorilla/websocket"
)

const (
	douyinBrowserStartupTimeout = 20 * time.Second
	douyinMediaCaptureTimeout   = 40 * time.Second
)

// Chromium does not permit two processes to mutate the same user-data
// directory. Queue Douyin captures so batch projects reuse one safe session
// instead of racing on the persisted KOVA profile lock.
var douyinBrowserSessionMu sync.Mutex

type devToolsMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type browserMediaCandidate struct {
	URL      string
	MIMEType string
	Score    int
}

type browserMediaSelection struct {
	Video browserMediaCandidate
	Audio browserMediaCandidate
}

type douyinProgress func(percent uint8, detail string)

// downloadDouyinSourceVideo uses Douyin's own browser application to perform
// the JavaScript signature/risk-control flow, then captures the real media
// response through the local Chrome DevTools Protocol. This intentionally does
// not reproduce a_bogus/__ac_signature in KOVA: those private web signatures
// change frequently, while the official page must always know how to create
// the currently valid request.
func downloadDouyinSourceVideo(ctx context.Context, link, videoPath, configuredBrowser string, progress douyinProgress) error {
	douyinBrowserSessionMu.Lock()
	defer douyinBrowserSessionMu.Unlock()

	var failures []string
	for _, browser := range shortVideoSessionBrowsers(configuredBrowser) {
		if progress != nil {
			progress(2, "Đang mở phiên "+browser+" riêng của KOVA")
		}
		if err := downloadDouyinWithBrowser(ctx, link, videoPath, browser, progress); err == nil {
			return nil
		} else {
			failures = append(failures, browser+": "+err.Error())
		}
	}
	if len(failures) == 0 {
		return errors.New("phiên trình duyệt Douyin của KOVA đang tắt")
	}
	return fmt.Errorf(
		"không bắt được luồng media đã ký của Douyin: %s. Hãy bấm “Thiết lập phiên Douyin”, hoàn tất xác minh trong cửa sổ trình duyệt KOVA, đóng cửa sổ đó rồi chạy lại",
		strings.Join(failures, " | "),
	)
}

func downloadDouyinWithBrowser(ctx context.Context, link, videoPath, browser string, progress douyinProgress) error {
	browserPath, err := findShortVideoSessionBrowser(browser)
	if err != nil {
		return err
	}
	profileDir, err := managedShortVideoProfileDir("douyin", browser)
	if err != nil {
		return err
	}
	port, err := reserveLoopbackPort()
	if err != nil {
		return err
	}

	command := exec.CommandContext(ctx, browserPath,
		"--headless=new",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port="+strconv.Itoa(port),
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-mode",
		"--disable-blink-features=AutomationControlled",
		"--autoplay-policy=no-user-gesture-required",
		"--window-size=1280,900",
		"about:blank",
	)
	processutil.HideConsole(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("khởi động phiên %s của KOVA: %w", browser, err)
	}
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()

	webSocketURL, err := waitForAnyDevToolsPage(ctx, port)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(8, "Trình duyệt KOVA đã sẵn sàng; đang để Douyin ký request")
	}
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, webSocketURL, nil)
	if err != nil {
		return fmt.Errorf("kết nối DevTools của phiên %s: %w", browser, err)
	}
	defer connection.Close()

	if err := sendSynchronousDevToolsCommand(connection, 1, "Network.enable", map[string]any{}); err != nil {
		return err
	}
	if err := sendSynchronousDevToolsCommand(connection, 2, "Page.enable", map[string]any{}); err != nil {
		return err
	}
	if err := connection.WriteJSON(map[string]any{
		"id":     3,
		"method": "Page.navigate",
		"params": map[string]any{"url": link},
	}); err != nil {
		return fmt.Errorf("mở URL Douyin trong phiên KOVA: %w", err)
	}

	messages := make(chan devToolsMessage, 256)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var message devToolsMessage
			if err := connection.ReadJSON(&message); err != nil {
				readErrors <- err
				return
			}
			messages <- message
		}
	}()

	selection, diagnostic, err := waitForDouyinMedia(ctx, connection, messages, readErrors)
	if err != nil {
		if diagnostic != "" {
			return fmt.Errorf("%w (%s)", err, diagnostic)
		}
		return err
	}
	if progress != nil {
		progress(20, "Đã bắt được luồng video và audio do Douyin ký")
	}

	cookies, err := requestDevToolsCookies(connection, messages)
	if err != nil {
		return err
	}
	userAgent, _ := requestDevToolsString(connection, messages, 10002, "navigator.userAgent")
	referer, _ := requestDevToolsString(connection, messages, 10003, "location.href")
	if strings.TrimSpace(referer) == "" {
		referer = link
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = shortVideoSessionUserAgent(browser)
	}
	if err := downloadCapturedMedia(ctx, selection, videoPath, referer, userAgent, cookies, progress); err != nil {
		return fmt.Errorf("tải luồng media Douyin đã ký: %w", err)
	}
	return nil
}

func waitForDouyinMedia(
	ctx context.Context,
	connection *websocket.Conn,
	messages <-chan devToolsMessage,
	readErrors <-chan error,
) (browserMediaSelection, string, error) {
	deadline := time.NewTimer(douyinMediaCaptureTimeout)
	defer deadline.Stop()
	evaluateTicker := time.NewTicker(750 * time.Millisecond)
	defer evaluateTicker.Stop()
	var best browserMediaSelection
	var firstCandidateAt time.Time
	var lastDiagnostic string
	nextID := 100

	for {
		if best.Video.URL != "" && !firstCandidateAt.IsZero() && time.Since(firstCandidateAt) >= 4*time.Second {
			return best, lastDiagnostic, nil
		}
		select {
		case <-ctx.Done():
			return browserMediaSelection{}, lastDiagnostic, ctx.Err()
		case <-deadline.C:
			if best.Video.URL != "" {
				return best, lastDiagnostic, nil
			}
			return browserMediaSelection{}, lastDiagnostic, errors.New("hết 40 giây nhưng trang Douyin chưa phát luồng video")
		case err := <-readErrors:
			if best.Video.URL != "" {
				return best, lastDiagnostic, nil
			}
			return browserMediaSelection{}, lastDiagnostic, fmt.Errorf("phiên trình duyệt Douyin kết thúc sớm: %w", err)
		case <-evaluateTicker.C:
			nextID++
			_ = connection.WriteJSON(map[string]any{
				"id":     nextID,
				"method": "Runtime.evaluate",
				"params": map[string]any{
					"expression": `(() => {
						const values = Array.from(document.querySelectorAll("video, video source"))
							.map((node) => node.currentSrc || node.src || "")
							.filter(Boolean);
						return JSON.stringify({
							media: values,
							url: location.href,
							title: document.title,
							text: (document.body && document.body.innerText || "").slice(0, 300)
						});
					})()`,
					"returnByValue": true,
				},
			})
		case message := <-messages:
			if message.Method == "Network.responseReceived" {
				var event struct {
					Type     string `json:"type"`
					Response struct {
						URL      string  `json:"url"`
						MIMEType string  `json:"mimeType"`
						Status   float64 `json:"status"`
					} `json:"response"`
				}
				if json.Unmarshal(message.Params, &event) == nil && event.Response.Status >= 200 && event.Response.Status < 400 {
					if os.Getenv("KOVA_DOUYIN_TRACE") == "1" &&
						(strings.EqualFold(event.Type, "Media") ||
							strings.HasPrefix(strings.ToLower(event.Response.MIMEType), "video/") ||
							strings.HasPrefix(strings.ToLower(event.Response.MIMEType), "audio/")) {
						fmt.Fprintf(os.Stderr, "KOVA_DOUYIN_MEDIA type=%s mime=%s url=%s\n", event.Type, event.Response.MIMEType, event.Response.URL)
					}
					videoCandidate := scoreBrowserMedia(event.Response.URL, event.Response.MIMEType, event.Type)
					if videoCandidate.Score > best.Video.Score {
						best.Video = videoCandidate
						if firstCandidateAt.IsZero() {
							firstCandidateAt = time.Now()
						}
					}
					audioCandidate := scoreBrowserAudio(event.Response.URL, event.Response.MIMEType, event.Type)
					if audioCandidate.Score > best.Audio.Score {
						best.Audio = audioCandidate
					}
				}
				continue
			}
			if message.ID < 100 || len(message.Result) == 0 {
				continue
			}
			var evaluation struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(message.Result, &evaluation) != nil || evaluation.Result.Value == "" {
				continue
			}
			var page struct {
				Media []string `json:"media"`
				URL   string   `json:"url"`
				Title string   `json:"title"`
				Text  string   `json:"text"`
			}
			if json.Unmarshal([]byte(evaluation.Result.Value), &page) != nil {
				continue
			}
			lastDiagnostic = compactDouyinDiagnostic(page.URL, page.Title, page.Text)
			for _, mediaURL := range page.Media {
				candidate := scoreBrowserMedia(mediaURL, "video/mp4", "Media")
				if candidate.Score > best.Video.Score {
					best.Video = candidate
					if firstCandidateAt.IsZero() {
						firstCandidateAt = time.Now()
					}
				}
			}
		}
	}
}

func scoreBrowserMedia(rawURL, mimeType, resourceType string) browserMediaCandidate {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "blob:") || strings.HasPrefix(rawURL, "data:") {
		return browserMediaCandidate{}
	}
	lowerURL := strings.ToLower(rawURL)
	lowerMIME := strings.ToLower(strings.TrimSpace(mimeType))
	if strings.Contains(lowerURL, "media-audio-") || strings.Contains(lowerURL, "/audio/tos/") {
		return browserMediaCandidate{}
	}
	score := 0
	if strings.HasPrefix(lowerMIME, "video/") {
		score += 100
	}
	if strings.EqualFold(resourceType, "Media") {
		score += 80
	}
	for _, marker := range []string{"douyinvod.com", "idouyinvod.com", "bytevcloud", "/video/tos/", "mime_type=video", ".mp4"} {
		if strings.Contains(lowerURL, marker) {
			score += 25
		}
	}
	if strings.Contains(lowerURL, "media-video-") {
		score += 160
	}
	if strings.Contains(lowerURL, "douyinstatic.com/obj/douyin-pc-web/") {
		score -= 150
	}
	if strings.HasPrefix(lowerMIME, "image/") || strings.HasPrefix(lowerMIME, "text/") ||
		strings.Contains(lowerURL, ".js") || strings.Contains(lowerURL, ".css") {
		return browserMediaCandidate{}
	}
	if score < 80 {
		return browserMediaCandidate{}
	}
	return browserMediaCandidate{URL: rawURL, MIMEType: mimeType, Score: score}
}

func scoreBrowserAudio(rawURL, mimeType, resourceType string) browserMediaCandidate {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "blob:") || strings.HasPrefix(rawURL, "data:") {
		return browserMediaCandidate{}
	}
	lowerURL := strings.ToLower(rawURL)
	lowerMIME := strings.ToLower(strings.TrimSpace(mimeType))
	explicitAudioURL := strings.Contains(lowerURL, "media-audio-") ||
		strings.Contains(lowerURL, "/audio/tos/") ||
		strings.Contains(lowerURL, "mime_type=audio")
	score := 0
	if explicitAudioURL {
		score += 160
	}
	if strings.HasPrefix(lowerMIME, "audio/") {
		score += 120
	}
	if strings.EqualFold(resourceType, "Media") {
		score += 40
	}
	for _, marker := range []string{"media-audio-", "mime_type=audio", "/audio/tos/", "audio_id=", "audio_mp4", "audio_mp3"} {
		if strings.Contains(lowerURL, marker) {
			score += 30
		}
	}
	if (strings.HasPrefix(lowerMIME, "video/") && !explicitAudioURL) || strings.HasPrefix(lowerMIME, "image/") ||
		strings.HasPrefix(lowerMIME, "text/") {
		return browserMediaCandidate{}
	}
	if score < 100 {
		return browserMediaCandidate{}
	}
	return browserMediaCandidate{URL: rawURL, MIMEType: mimeType, Score: score}
}

func compactDouyinDiagnostic(pageURL, title, text string) string {
	combined := strings.Join([]string{strings.TrimSpace(title), strings.TrimSpace(text)}, " ")
	combined = strings.Join(strings.Fields(combined), " ")
	if len(combined) > 220 {
		combined = combined[:220] + "…"
	}
	if strings.Contains(strings.ToLower(pageURL+" "+combined), "captcha") ||
		strings.Contains(combined, "验证") ||
		strings.Contains(strings.ToLower(combined), "verify") {
		return "Douyin đang yêu cầu xác minh trong phiên trình duyệt KOVA"
	}
	if combined == "" {
		return "trang Douyin không trả nội dung video"
	}
	return combined
}

func requestDevToolsCookies(connection *websocket.Conn, messages <-chan devToolsMessage) ([]browserCookie, error) {
	result, err := requestDevToolsResult(connection, messages, 10001, "Network.getAllCookies", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Cookies []browserCookie `json:"cookies"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("đọc cookie phiên Douyin của KOVA: %w", err)
	}
	return payload.Cookies, nil
}

func requestDevToolsString(connection *websocket.Conn, messages <-chan devToolsMessage, id int, expression string) (string, error) {
	result, err := requestDevToolsResult(connection, messages, id, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var evaluation struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &evaluation); err != nil {
		return "", err
	}
	return strings.TrimSpace(evaluation.Result.Value), nil
}

func requestDevToolsResult(connection *websocket.Conn, messages <-chan devToolsMessage, id int, method string, params map[string]any) (json.RawMessage, error) {
	if err := connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil, fmt.Errorf("DevTools không phản hồi %s", method)
		case message := <-messages:
			if message.ID != id {
				continue
			}
			if message.Error != nil {
				return nil, errors.New(message.Error.Message)
			}
			return message.Result, nil
		}
	}
}

func downloadCapturedMedia(ctx context.Context, selection browserMediaSelection, videoPath, referer, userAgent string, cookies []browserCookie, progress douyinProgress) error {
	videoEnd := uint8(88)
	if selection.Audio.URL != "" && selection.Audio.URL != selection.Video.URL {
		videoEnd = 72
	}
	videoInput, err := fetchCapturedMedia(ctx, selection.Video.URL, filepath.Dir(videoPath), referer, userAgent, cookies, 22, videoEnd, progress)
	if err != nil {
		return err
	}
	defer os.Remove(videoInput)

	audioInput := ""
	if selection.Audio.URL != "" && selection.Audio.URL != selection.Video.URL {
		audioInput, err = fetchCapturedMedia(ctx, selection.Audio.URL, filepath.Dir(videoPath), referer, userAgent, cookies, 73, 90, progress)
		if err != nil {
			return fmt.Errorf("tải audio đi kèm: %w", err)
		}
		defer os.Remove(audioInput)
	}

	commandArgs := []string{"-y", "-i", videoInput}
	if audioInput != "" {
		commandArgs = append(commandArgs, "-i", audioInput, "-map", "0:v:0", "-map", "1:a:0")
	} else {
		commandArgs = append(commandArgs, "-map", "0:v:0", "-map", "0:a?")
	}
	commandArgs = append(commandArgs, "-c", "copy", "-movflags", "+faststart", videoPath)
	if progress != nil {
		progress(94, "Đang ghép luồng video và audio thành MP4")
	}
	command := exec.CommandContext(ctx, storage.FfmpegPath, commandArgs...)
	processutil.HideConsole(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg không đóng gói được media Douyin: %w: %s", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(videoPath)
	if err != nil || info.IsDir() || info.Size() < 64*1024 {
		if err == nil {
			err = errors.New("file đầu ra rỗng")
		}
		return fmt.Errorf("video Douyin đầu ra không hợp lệ: %w", err)
	}
	if progress != nil {
		progress(99, "Video Douyin đã tải và ghép đủ hình/tiếng")
	}
	return nil
}

func fetchCapturedMedia(
	ctx context.Context,
	mediaURL, directory, referer, userAgent string,
	cookies []browserCookie,
	startPercent, endPercent uint8,
	progress douyinProgress,
) (string, error) {
	parsed, err := url.Parse(mediaURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", errors.New("URL media do trình duyệt bắt được không hợp lệ")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", referer)
	if cookieHeader := browserCookieHeader(cookies); cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
	client := &http.Client{Timeout: 12 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("HTTP %d từ máy chủ media", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json") {
		return "", fmt.Errorf("máy chủ trả %s thay vì media", contentType)
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".kova-douyin-media-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	written, copyErr := copyMediaWithProgress(temporary, response.Body, response.ContentLength, startPercent, endPercent, progress)
	closeErr := temporary.Close()
	if copyErr != nil {
		_ = os.Remove(temporaryPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporaryPath)
		return "", closeErr
	}
	if written < 16*1024 {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("luồng media quá nhỏ (%d byte), có thể là trang chặn", written)
	}
	return temporaryPath, nil
}

func copyMediaWithProgress(
	destination io.Writer,
	source io.Reader,
	total int64,
	startPercent, endPercent uint8,
	progress douyinProgress,
) (int64, error) {
	buffer := make([]byte, 256*1024)
	var written int64
	lastPercent := uint8(255)
	lastUpdate := time.Time{}
	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			n, writeErr := destination.Write(buffer[:count])
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != count {
				return written, io.ErrShortWrite
			}
			if progress != nil {
				percent := startPercent
				if total > 0 && endPercent > startPercent {
					fraction := float64(written) / float64(total)
					if fraction > 1 {
						fraction = 1
					}
					percent += uint8(float64(endPercent-startPercent) * fraction)
				}
				if percent != lastPercent && (lastUpdate.IsZero() || time.Since(lastUpdate) >= 350*time.Millisecond || percent == endPercent) {
					progress(percent, fmt.Sprintf("Đang tải media Douyin: %.1f MB", float64(written)/(1024*1024)))
					lastPercent = percent
					lastUpdate = time.Now()
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if progress != nil {
					progress(endPercent, fmt.Sprintf("Đã tải %.1f MB", float64(written)/(1024*1024)))
				}
				return written, nil
			}
			return written, readErr
		}
	}
}

func browserCookieHeader(cookies []browserCookie) string {
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if strings.TrimSpace(cookie.Name) == "" || strings.ContainsAny(cookie.Name+cookie.Value, "\r\n;") {
			continue
		}
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(values, "; ")
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("giữ cổng DevTools nội bộ: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

func waitForAnyDevToolsPage(ctx context.Context, port int) (string, error) {
	deadline := time.Now().Add(douyinBrowserStartupTimeout)
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + "/json/list"
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		response, err := client.Get(endpoint)
		if err == nil {
			var pages []browserDevToolsPage
			decodeErr := json.NewDecoder(response.Body).Decode(&pages)
			_ = response.Body.Close()
			if decodeErr == nil {
				for _, page := range pages {
					if page.Type == "page" && page.WebSocketDebuggerURL != "" {
						return page.WebSocketDebuggerURL, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", errors.New("trình duyệt KOVA không mở được cổng DevTools nội bộ")
}

func sendSynchronousDevToolsCommand(connection *websocket.Conn, id int, method string, params map[string]any) error {
	if err := connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	_ = connection.SetReadDeadline(time.Now().Add(8 * time.Second))
	defer connection.SetReadDeadline(time.Time{})
	for {
		var message devToolsMessage
		if err := connection.ReadJSON(&message); err != nil {
			return err
		}
		if message.ID != id {
			continue
		}
		if message.Error != nil {
			return errors.New(message.Error.Message)
		}
		return nil
	}
}

func managedShortVideoProfileDir(platform, browser string) (string, error) {
	root := strings.TrimSpace(os.Getenv("KOVA_DATA_DIR"))
	if root == "" {
		configRoot, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("tìm thư mục dữ liệu KOVA: %w", err)
		}
		root = filepath.Join(configRoot, "KOVA")
	}
	directory := filepath.Join(root, "browser-sessions", platform, strings.ToLower(strings.TrimSpace(browser)))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", fmt.Errorf("tạo profile trình duyệt KOVA: %w", err)
	}
	return directory, nil
}

// OpenManagedShortVideoSession opens the persistent, KOVA-owned browser
// profile. The user may complete Douyin/TikTok login or CAPTCHA there once;
// later headless downloads reuse that session without reading the user's
// personal Chrome/Edge profile or exporting cookies through the UI.
func OpenManagedShortVideoSession(link, configuredBrowser string) error {
	link = strings.TrimSpace(link)
	if !isCookieRestrictedShortVideoURL(link) {
		return errors.New("hãy nhập URL Douyin hoặc TikTok trước khi thiết lập phiên")
	}
	browsers := shortVideoSessionBrowsers(configuredBrowser)
	if len(browsers) == 0 {
		browsers = []string{"edge", "chrome"}
	}
	var failures []string
	for _, browser := range browsers {
		browserPath, err := findShortVideoSessionBrowser(browser)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		platform := "tiktok"
		if isDouyinURL(link) {
			platform = "douyin"
		}
		profileDir, err := managedShortVideoProfileDir(platform, browser)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		command := exec.Command(browserPath,
			"--user-data-dir="+profileDir,
			"--no-first-run",
			"--no-default-browser-check",
			"--new-window",
			link,
		)
		processutil.HideConsole(command)
		if err := command.Start(); err != nil {
			failures = append(failures, browser+": "+err.Error())
			continue
		}
		return nil
	}
	return fmt.Errorf("không mở được phiên trình duyệt KOVA: %s", strings.Join(failures, " | "))
}

func isDouyinURL(link string) bool {
	lower := strings.ToLower(strings.TrimSpace(link))
	return strings.Contains(lower, "douyin.com") || strings.Contains(lower, "iesdouyin.com")
}

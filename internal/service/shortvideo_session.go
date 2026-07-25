package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kova/internal/processutil"

	"github.com/gorilla/websocket"
)

const (
	shortVideoBrowserStartupTimeout = 18 * time.Second
	shortVideoCookieWait            = 4 * time.Second
)

type browserDevToolsPage struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type browserDevToolsReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
}

type browserCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
}

// createFreshShortVideoCookieFile starts an isolated headless browser profile
// and turns only the new Douyin/TikTok session cookies into an ephemeral
// Netscape file for yt-dlp. It never opens an existing Chrome/Edge profile:
// this avoids Windows App-Bound DPAPI and guarantees the caller cannot access
// personal browser cookies. The returned cleanup removes both the temporary
// profile and the cookie file as soon as the retry command has finished.
func createFreshShortVideoCookieFile(ctx context.Context, link, preferredBrowser string) (string, func(), error) {
	browserPath, err := findShortVideoSessionBrowser(preferredBrowser)
	if err != nil {
		return "", func() {}, err
	}
	profileDir, err := os.MkdirTemp("", "kova-shortvideo-session-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary browser profile: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(profileDir) }

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("reserve local browser debugging port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	command := exec.CommandContext(ctx, browserPath,
		"--headless=new",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port="+strconv.Itoa(port),
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-gpu",
		"--disable-blink-features=AutomationControlled",
		"--user-agent="+shortVideoSessionUserAgent(browserPath),
		"--window-size=1280,720",
		link,
	)
	processutil.HideConsole(command)
	if err := command.Start(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("start temporary browser session: %w", err)
	}
	defer func() {
		// This is the process KOVA just started with a unique temporary profile;
		// it cannot target the user's normal browser process.
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()

	pageURL, err := waitForShortVideoDevToolsPage(ctx, port, link)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	select {
	case <-ctx.Done():
		cleanup()
		return "", func() {}, ctx.Err()
	case <-time.After(shortVideoCookieWait):
	}
	cookies, err := readShortVideoCookies(ctx, pageURL)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	path, err := writeShortVideoCookieFile(profileDir, cookies)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func shortVideoSessionUserAgent(browser string) string {
	// This value is used by an isolated, fresh browser profile and by yt-dlp
	// immediately afterwards. It never reads or copies a user's browser
	// fingerprint or profile setting.
	if strings.Contains(strings.ToLower(browser), "chrome") {
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0"
}

func findShortVideoSessionBrowser(preferred string) (string, error) {
	programFiles := []string{os.Getenv("PROGRAMFILES(X86)"), os.Getenv("PROGRAMFILES"), os.Getenv("LOCALAPPDATA")}
	pathsFor := func(browser string) []string {
		switch browser {
		case "chrome":
			paths := []string{"chrome.exe", "chrome"}
			for _, root := range programFiles {
				if strings.TrimSpace(root) != "" {
					paths = append(paths, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
				}
			}
			return paths
		default:
			paths := []string{"msedge.exe", "msedge"}
			for _, root := range programFiles {
				if strings.TrimSpace(root) != "" {
					paths = append(paths, filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
				}
			}
			return paths
		}
	}
	for _, candidate := range pathsFor(strings.ToLower(strings.TrimSpace(preferred))) {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("không tìm thấy %s để tạo phiên tải Douyin/TikTok tạm", preferred)
}

func waitForShortVideoDevToolsPage(ctx context.Context, port int, link string) (string, error) {
	deadline := time.Now().Add(shortVideoBrowserStartupTimeout)
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
					if page.Type == "page" && page.WebSocketDebuggerURL != "" && (strings.Contains(page.URL, "douyin.com") || strings.Contains(page.URL, "tiktok.com") || page.URL == link) {
						return page.WebSocketDebuggerURL, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", errors.New("temporary browser session did not expose its local page")
}

func readShortVideoCookies(ctx context.Context, webSocketURL string) ([]browserCookie, error) {
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, webSocketURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect temporary browser session: %w", err)
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]any{"id": 1, "method": "Network.getAllCookies"}); err != nil {
		return nil, fmt.Errorf("request temporary browser cookies: %w", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(8 * time.Second))
	for {
		var reply browserDevToolsReply
		if err := connection.ReadJSON(&reply); err != nil {
			return nil, fmt.Errorf("read temporary browser cookies: %w", err)
		}
		if reply.ID != 1 {
			continue
		}
		var payload struct {
			Cookies []browserCookie `json:"cookies"`
		}
		if err := json.Unmarshal(reply.Result, &payload); err != nil {
			return nil, fmt.Errorf("decode temporary browser cookies: %w", err)
		}
		filtered := make([]browserCookie, 0, len(payload.Cookies))
		for _, cookie := range payload.Cookies {
			domain := strings.ToLower(cookie.Domain)
			if strings.Contains(domain, "douyin.com") || strings.Contains(domain, "tiktok.com") || strings.Contains(domain, "iesdouyin.com") {
				filtered = append(filtered, cookie)
			}
		}
		if len(filtered) == 0 {
			return nil, errors.New("temporary browser did not receive a Douyin/TikTok session cookie")
		}
		return filtered, nil
	}
}

func writeShortVideoCookieFile(directory string, cookies []browserCookie) (string, error) {
	path := filepath.Join(directory, "kova-shortvideo-cookies.txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("create temporary cookie file: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString("# Netscape HTTP Cookie File\n"); err != nil {
		return "", err
	}
	written := 0
	for _, cookie := range cookies {
		if strings.ContainsAny(cookie.Name+cookie.Value+cookie.Domain+cookie.Path, "\r\n\t") || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Domain) == "" {
			continue
		}
		includeSubdomains := "FALSE"
		if strings.HasPrefix(cookie.Domain, ".") {
			includeSubdomains = "TRUE"
		}
		secure := "FALSE"
		if cookie.Secure {
			secure = "TRUE"
		}
		expires := int64(0)
		if cookie.Expires > 0 {
			expires = int64(cookie.Expires)
		}
		if _, err := fmt.Fprintf(file, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", cookie.Domain, includeSubdomains, firstNonEmptyCookiePath(cookie.Path), secure, expires, cookie.Name, cookie.Value); err != nil {
			return "", err
		}
		written++
	}
	if written == 0 {
		return "", errors.New("temporary browser session returned no usable cookies")
	}
	return path, nil
}

func firstNonEmptyCookiePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	return path
}

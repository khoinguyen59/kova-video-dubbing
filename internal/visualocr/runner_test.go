package visualocr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodePreflightResultUsesLastJSONLine(t *testing.T) {
	result, err := decodePreflightResult("Paddle startup log\n{\"ready\":true,\"cuda_available\":true,\"python\":\"C:/Python/python.exe\"}\n")
	if err != nil {
		t.Fatalf("decodePreflightResult() error = %v", err)
	}
	if !result.Ready || !result.CUDAAvailable || result.Python == "" {
		t.Fatalf("decodePreflightResult() = %#v", result)
	}
}

func TestResolveBridgeAcceptsConfiguredBridgePath(t *testing.T) {
	dir := t.TempDir()
	bridge := filepath.Join(dir, "kova_visual_ocr.py")
	if err := os.WriteFile(bridge, []byte("# bridge fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	python, script, err := (Runner{Config: Config{PythonPath: "python-test", ScriptPath: bridge}}).resolveBridge()
	if err != nil {
		t.Fatalf("resolveBridge() error = %v", err)
	}
	if python != "python-test" || script != bridge {
		t.Fatalf("resolveBridge() = (%q, %q), want (%q, %q)", python, script, "python-test", bridge)
	}
}

package visualocr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestColabNotebookIsolatesTorchFromPaddleGPU(t *testing.T) {
	notebookPath := filepath.Join("..", "..", "notebooks", "KOVA_VISUAL_OCR_GPU.ipynb")
	data, err := os.ReadFile(notebookPath)
	if err != nil {
		t.Fatalf("read OCR Colab notebook: %v", err)
	}

	content := string(data)
	required := []string{
		"/content/kova-ocr-venv",
		"https://download.pytorch.org/whl/cpu",
		"paddlepaddle-gpu==3.3.0",
		"https://www.paddlepaddle.org.cn/packages/stable/cu126/",
		"worker = subprocess.Popen([PYTHON",
	}
	for _, fragment := range required {
		if !strings.Contains(content, fragment) {
			t.Errorf("OCR Colab notebook is missing isolation contract %q", fragment)
		}
	}

	forbidden := []string{
		"pip uninstall --yes paddlepaddle",
		"worker = subprocess.Popen([sys.executable",
	}
	for _, fragment := range forbidden {
		if strings.Contains(content, fragment) {
			t.Errorf("OCR Colab notebook contains unsafe global-runtime command %q", fragment)
		}
	}
}

package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	wailsassets "github.com/wailsapp/wails/v2/pkg/assetserver"
	assetoptions "github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func TestEmbeddedFrontendServesViteModule(t *testing.T) {
	frontendDist, err := fs.Sub(frontendAssets, "frontend/dist")
	if err != nil {
		t.Fatalf("frontend dist sub-filesystem: %v", err)
	}
	handler, err := wailsassets.NewAssetHandler(assetoptions.Options{Assets: frontendDist}, nil)
	if err != nil {
		t.Fatalf("create frontend asset handler: %v", err)
	}

	indexResponse := httptest.NewRecorder()
	handler.ServeHTTP(indexResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", indexResponse.Code)
	}

	modulePath := regexp.MustCompile(`src="(/assets/[^"?]+\.js)"`).FindStringSubmatch(indexResponse.Body.String())
	if len(modulePath) != 2 {
		t.Fatal("index does not reference a Vite JavaScript module")
	}

	moduleResponse := httptest.NewRecorder()
	handler.ServeHTTP(moduleResponse, httptest.NewRequest(http.MethodGet, modulePath[1], nil))
	if moduleResponse.Code != http.StatusOK {
		t.Fatalf("module %s status = %d, want 200", modulePath[1], moduleResponse.Code)
	}
	if !strings.Contains(moduleResponse.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("module Content-Type = %q, want JavaScript", moduleResponse.Header().Get("Content-Type"))
	}
	if !strings.Contains(moduleResponse.Body.String(), "KOVA") {
		t.Fatal("module does not contain the KOVA frontend")
	}
}

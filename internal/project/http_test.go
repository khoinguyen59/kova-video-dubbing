package project

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestV2HTTPWorkflowRequiresExplicitReview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHTTPHandler(filepath.Join(t.TempDir(), "kova.db"))
	t.Cleanup(func() { _ = handler.Close() })
	RegisterRoutes(router.Group("/api/v2"), handler)

	project := requestJSON(t, router, http.MethodPost, "/api/v2/projects", `{"name":"KOVA test","target_language":"vi"}`)
	if project.Code != http.StatusCreated {
		t.Fatalf("create project = %d: %s", project.Code, project.Body.String())
	}
	var created struct {
		Data Project `json:"data"`
	}
	if err := json.Unmarshal(project.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	blocked := requestJSON(t, router, http.MethodPost, "/api/v2/projects/"+created.Data.ID+"/stages/translation/start", "{}")
	if blocked.Code != http.StatusConflict {
		t.Fatalf("translation before approval = %d: %s", blocked.Code, blocked.Body.String())
	}
	source := requestJSON(t, router, http.MethodPost, "/api/v2/projects/"+created.Data.ID+"/stages/source/start", "{}")
	if source.Code != http.StatusAccepted {
		t.Fatalf("source start = %d: %s", source.Code, source.Body.String())
	}
	var sourceReply struct {
		Data StageRun `json:"data"`
	}
	if err := json.Unmarshal(source.Body.Bytes(), &sourceReply); err != nil {
		t.Fatal(err)
	}
	review := requestJSON(t, router, http.MethodPost, "/api/v2/stage-runs/"+sourceReply.Data.ID+"/review", `{"message_key":"stage.source.ready"}`)
	if review.Code != http.StatusOK {
		t.Fatalf("review = %d: %s", review.Code, review.Body.String())
	}
	approved := requestJSON(t, router, http.MethodPost, "/api/v2/stage-runs/"+sourceReply.Data.ID+"/approve", "{}")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", approved.Code, approved.Body.String())
	}
	translation := requestJSON(t, router, http.MethodPost, "/api/v2/projects/"+created.Data.ID+"/stages/translation/start", "{}")
	if translation.Code != http.StatusAccepted {
		t.Fatalf("translation after approval = %d: %s", translation.Code, translation.Body.String())
	}
}

func requestJSON(t *testing.T, router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

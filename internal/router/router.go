package router

import (
	"kova/internal/handler"
	"kova/internal/project"
	"kova/static"
	"net/http"

	"github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
	api := r.Group("/api")
	project.RegisterRoutes(api.Group("/v2"), project.NewHTTPHandler(project.DefaultDatabasePath()))

	hdl := handler.NewHandler()
	{
		// Kova API v1. Desktop and new integrations use these stable routes.
		api.GET("/v1/status", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"error": 0,
				"msg":   "Kova API ready",
				"data": gin.H{
					"name":        "Kova",
					"api_version": "v1",
					"capabilities": []string{
						"video-source",
						"visual-subtitle-ocr",
						"subtitle-translation",
						"fixed-voice-dubbing",
						"video-render",
					},
				},
			})
		})
		api.POST("/v1/jobs/subtitle", hdl.StartSubtitleTask)
		api.GET("/v1/jobs/subtitle", hdl.GetSubtitleTask)
		// Native staged workflow. Each POST runs exactly one user-selected step;
		// the server rejects later steps until the preceding review is approved.
		api.POST("/v1/jobs/subtitle/stages/source", hdl.StartWorkflowSource)
		api.GET("/v1/jobs/subtitle/:taskId/workflow", hdl.GetWorkflow)
		api.PUT("/v1/jobs/subtitle/:taskId/subtitles/:kind", hdl.UpdateWorkflowSubtitle)
		api.POST("/v1/jobs/subtitle/:taskId/source/approve", hdl.ApproveWorkflowSource)
		api.POST("/v1/jobs/subtitle/:taskId/translation", hdl.StartWorkflowTranslation)
		api.POST("/v1/jobs/subtitle/:taskId/translation/approve", hdl.ApproveWorkflowTranslation)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing", hdl.StartWorkflowDubbing)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/skip", hdl.SkipWorkflowDubbing)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/approve", hdl.ApproveWorkflowDubbing)
		// Explicit review-first dubbing stages. The short /dubbing paths above
		// remain compatibility aliases for the audio-only half; they never mux.
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/audio", hdl.StartWorkflowDubbingAudio)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/audio/approve", hdl.ApproveWorkflowDubbingAudio)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/video", hdl.StartWorkflowDubbingVideo)
		api.POST("/v1/jobs/subtitle/:taskId/dubbing/video/approve", hdl.ApproveWorkflowDubbingVideo)
		api.POST("/v1/jobs/subtitle/:taskId/render", hdl.StartWorkflowRender)
		api.POST("/v1/files", hdl.UploadFile)
		api.GET("/v1/files/*filepath", hdl.DownloadFile)
		api.HEAD("/v1/files/*filepath", hdl.DownloadFile)
		api.GET("/v1/config", hdl.GetConfig)
		api.PUT("/v1/config", hdl.UpdateConfig)

		// Deprecated compatibility aliases for automation written before Kova v1.
		api.POST("/capability/subtitleTask", hdl.StartSubtitleTask)
		api.GET("/capability/subtitleTask", hdl.GetSubtitleTask)
		api.POST("/file", hdl.UploadFile)
		api.GET("/file/*filepath", hdl.DownloadFile)
		api.HEAD("/file/*filepath", hdl.DownloadFile)
		api.GET("/config", hdl.GetConfig)
		api.POST("/config", hdl.UpdateConfig)
	}

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/static")
	})
	r.StaticFS("/static", http.FS(static.EmbeddedFiles))
}

// Package project stores the KOVA v2 project timeline. It is intentionally
// separate from legacy subtitle_task persistence so the Wails client can move
// to immutable artifacts without breaking existing v1 jobs.
package project

import "time"

type Stage string

const (
	StageSource       Stage = "source"
	StageTranslation  Stage = "translation"
	StageDubbingAudio Stage = "dubbing_audio"
	StageRender       Stage = "render"
	StageOutputs      Stage = "outputs"
)

var OrderedStages = []Stage{StageSource, StageTranslation, StageDubbingAudio, StageRender, StageOutputs}

type StageStatus string

const (
	StatusNotStarted   StageStatus = "not_started"
	StatusQueued       StageStatus = "queued"
	StatusRunning      StageStatus = "running"
	StatusReviewNeeded StageStatus = "review_required"
	StatusApproved     StageStatus = "approved"
	StatusStale        StageStatus = "stale"
	StatusFailed       StageStatus = "failed"
	StatusCancelled    StageStatus = "cancelled"
)

type Project struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	TargetLanguage string    `json:"target_language"`
	WorkflowTaskID string    `json:"workflow_task_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type StageRun struct {
	ID            string      `json:"id"`
	ProjectID     string      `json:"project_id"`
	Stage         Stage       `json:"stage"`
	Status        StageStatus `json:"status"`
	InputRevision int         `json:"input_revision"`
	MessageKey    string      `json:"message_key"`
	FailureCode   string      `json:"failure_code,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type Artifact struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	StageRunID string    `json:"stage_run_id"`
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	Checksum   string    `json:"checksum"`
	Revision   int       `json:"revision"`
	CreatedAt  time.Time `json:"created_at"`
}

type Snapshot struct {
	Project   Project    `json:"project"`
	StageRuns []StageRun `json:"stage_runs"`
	Artifacts []Artifact `json:"artifacts"`
}

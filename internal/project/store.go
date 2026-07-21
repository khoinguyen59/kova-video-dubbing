package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var (
	ErrProjectNotFound   = errors.New("project not found")
	ErrInvalidStage      = errors.New("invalid stage")
	ErrPrerequisite      = errors.New("previous stage has not been approved")
	ErrInvalidTransition = errors.New("invalid stage transition")
)

type Store struct {
	db *sql.DB
}

func Open(databasePath string) (*Store, error) {
	databasePath = strings.TrimSpace(databasePath)
	if databasePath == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0755); err != nil {
		return nil, fmt.Errorf("create Kova data directory: %w", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			target_language TEXT NOT NULL,
			workflow_task_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS stage_runs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			stage TEXT NOT NULL,
			status TEXT NOT NULL,
			input_revision INTEGER NOT NULL DEFAULT 1,
			message_key TEXT NOT NULL DEFAULT '',
			failure_code TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS stage_runs_project_stage_idx ON stage_runs(project_id, stage, created_at DESC);
		CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			stage_run_id TEXT NOT NULL REFERENCES stage_runs(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			checksum TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS artifacts_project_idx ON artifacts(project_id, created_at DESC);
	`)
	if err != nil {
		return err
	}
	columns, err := s.projectColumns(ctx)
	if err != nil {
		return err
	}
	if !columns["workflow_task_id"] {
		_, err = s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN workflow_task_id TEXT NOT NULL DEFAULT ''`)
	}
	return err
}

func (s *Store) CreateProject(ctx context.Context, name, targetLanguage string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	targetLanguage = strings.TrimSpace(targetLanguage)
	if targetLanguage == "" {
		targetLanguage = "vi"
	}
	now := time.Now().UTC()
	project := Project{ID: uuid.NewString(), Name: name, TargetLanguage: targetLanguage, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(id, name, target_language, workflow_task_id, created_at, updated_at) VALUES (?, ?, ?, '', ?, ?)`, project.ID, project.Name, project.TargetLanguage, timestamp(project.CreatedAt), timestamp(project.UpdatedAt))
	return project, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, target_language, workflow_task_id, created_at, updated_at FROM projects ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, project)
	}
	return result, rows.Err()
}

func (s *Store) Snapshot(ctx context.Context, projectID string) (Snapshot, error) {
	project, err := s.project(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	runs, err := s.stageRuns(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	artifacts, err := s.artifacts(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Project: project, StageRuns: runs, Artifacts: artifacts}, nil
}

func (s *Store) StartStage(ctx context.Context, projectID string, stage Stage) (StageRun, error) {
	if !knownStage(stage) {
		return StageRun{}, ErrInvalidStage
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StageRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireProject(ctx, tx, projectID); err != nil {
		return StageRun{}, err
	}
	if previous := previousStage(stage); previous != "" {
		status, err := latestStageStatus(ctx, tx, projectID, previous)
		if err != nil {
			return StageRun{}, err
		}
		if status != StatusApproved {
			return StageRun{}, ErrPrerequisite
		}
	}
	if err := markDownstreamStale(ctx, tx, projectID, stage); err != nil {
		return StageRun{}, err
	}
	now := time.Now().UTC()
	run := StageRun{ID: uuid.NewString(), ProjectID: projectID, Stage: stage, Status: StatusRunning, InputRevision: nextRevision(ctx, tx, projectID, stage), MessageKey: "stage.running", CreatedAt: now, UpdatedAt: now}
	_, err = tx.ExecContext(ctx, `INSERT INTO stage_runs(id, project_id, stage, status, input_revision, message_key, failure_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, '', ?, ?)`, run.ID, run.ProjectID, run.Stage, run.Status, run.InputRevision, run.MessageKey, timestamp(run.CreatedAt), timestamp(run.UpdatedAt))
	if err != nil {
		return StageRun{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE id = ?`, timestamp(now), projectID)
	if err != nil {
		return StageRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return StageRun{}, err
	}
	return run, nil
}

func (s *Store) MarkReviewRequired(ctx context.Context, runID string, messageKey string) (StageRun, error) {
	return s.transition(ctx, runID, StatusRunning, StatusReviewNeeded, messageKey, "")
}

// SetWorkflowTaskID associates the review-first KOVA project with the legacy
// worker task after the user explicitly starts the source stage. The ID is
// workflow metadata only; it contains no credential or media path.
func (s *Store) SetWorkflowTaskID(ctx context.Context, projectID, workflowTaskID string) (Project, error) {
	projectID, workflowTaskID = strings.TrimSpace(projectID), strings.TrimSpace(workflowTaskID)
	if projectID == "" || workflowTaskID == "" {
		return Project{}, errors.New("project id and workflow task id are required")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET workflow_task_id = ?, updated_at = ? WHERE id = ?`, workflowTaskID, timestamp(now), projectID)
	if err != nil {
		return Project{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Project{}, err
	}
	if count != 1 {
		return Project{}, ErrProjectNotFound
	}
	return s.project(ctx, projectID)
}

func (s *Store) ApproveStage(ctx context.Context, runID string) (StageRun, error) {
	return s.transition(ctx, runID, StatusReviewNeeded, StatusApproved, "stage.approved", "")
}

func (s *Store) FailStage(ctx context.Context, runID, failureCode string) (StageRun, error) {
	return s.transition(ctx, runID, StatusRunning, StatusFailed, "stage.failed", strings.TrimSpace(failureCode))
}

// SetFailureDetail replaces a generic failure marker after a background
// worker has returned its actionable diagnosis. It only applies to a run that
// has already entered the failed state.
func (s *Store) SetFailureDetail(ctx context.Context, runID, failureCode string) (StageRun, error) {
	runID, failureCode = strings.TrimSpace(runID), strings.TrimSpace(failureCode)
	if runID == "" || failureCode == "" {
		return StageRun{}, errors.New("run id and failure detail are required")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE stage_runs SET failure_code = ?, updated_at = ? WHERE id = ? AND status = ?`, failureCode, timestamp(now), runID, StatusFailed)
	if err != nil {
		return StageRun{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StageRun{}, err
	}
	if count != 1 {
		return StageRun{}, ErrInvalidTransition
	}
	return s.run(ctx, runID)
}

func (s *Store) CreateArtifact(ctx context.Context, artifact Artifact) (Artifact, error) {
	artifact.ProjectID = strings.TrimSpace(artifact.ProjectID)
	artifact.StageRunID = strings.TrimSpace(artifact.StageRunID)
	artifact.Kind = strings.TrimSpace(artifact.Kind)
	artifact.Path = filepath.ToSlash(strings.TrimSpace(artifact.Path))
	if artifact.ProjectID == "" || artifact.StageRunID == "" || artifact.Kind == "" || !safeArtifactPath(artifact.Path) {
		return Artifact{}, errors.New("invalid artifact")
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	if artifact.Revision < 1 {
		artifact.Revision = 1
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(id, project_id, stage_run_id, kind, path, checksum, revision, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS(SELECT 1 FROM stage_runs WHERE id = ? AND project_id = ?)`, artifact.ID, artifact.ProjectID, artifact.StageRunID, artifact.Kind, artifact.Path, artifact.Checksum, artifact.Revision, timestamp(artifact.CreatedAt), artifact.StageRunID, artifact.ProjectID)
	if err != nil {
		return Artifact{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Artifact{}, err
	}
	if count != 1 {
		return Artifact{}, ErrProjectNotFound
	}
	return artifact, nil
}

func (s *Store) transition(ctx context.Context, runID string, from, to StageStatus, messageKey, failureCode string) (StageRun, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE stage_runs SET status = ?, message_key = ?, failure_code = ?, updated_at = ? WHERE id = ? AND status = ?`, to, messageKey, failureCode, timestamp(now), runID, from)
	if err != nil {
		return StageRun{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StageRun{}, err
	}
	if count != 1 {
		return StageRun{}, ErrInvalidTransition
	}
	return s.run(ctx, runID)
}

func (s *Store) project(ctx context.Context, id string) (Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, target_language, workflow_task_id, created_at, updated_at FROM projects WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	return project, err
}

func (s *Store) stageRuns(ctx context.Context, projectID string) ([]StageRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project_id, stage, status, input_revision, message_key, failure_code, created_at, updated_at FROM stage_runs WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []StageRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (s *Store) artifacts(ctx context.Context, projectID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project_id, stage_run_id, kind, path, checksum, revision, created_at FROM artifacts WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Artifact
	for rows.Next() {
		var artifact Artifact
		var createdAt string
		if err := rows.Scan(&artifact.ID, &artifact.ProjectID, &artifact.StageRunID, &artifact.Kind, &artifact.Path, &artifact.Checksum, &artifact.Revision, &createdAt); err != nil {
			return nil, err
		}
		artifact.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}

func (s *Store) run(ctx context.Context, id string) (StageRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, project_id, stage, status, input_revision, message_key, failure_code, created_at, updated_at FROM stage_runs WHERE id = ?`, id)
	return scanRun(row)
}

type scanner interface{ Scan(...any) error }

func scanProject(row scanner) (Project, error) {
	var project Project
	var createdAt, updatedAt string
	if err := row.Scan(&project.ID, &project.Name, &project.TargetLanguage, &project.WorkflowTaskID, &createdAt, &updatedAt); err != nil {
		return Project{}, err
	}
	var err error
	if project.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return Project{}, err
	}
	if project.UpdatedAt, err = parseTimestamp(updatedAt); err != nil {
		return Project{}, err
	}
	return project, nil
}

func scanRun(row scanner) (StageRun, error) {
	var run StageRun
	var createdAt, updatedAt string
	if err := row.Scan(&run.ID, &run.ProjectID, &run.Stage, &run.Status, &run.InputRevision, &run.MessageKey, &run.FailureCode, &createdAt, &updatedAt); err != nil {
		return StageRun{}, err
	}
	var err error
	if run.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return StageRun{}, err
	}
	if run.UpdatedAt, err = parseTimestamp(updatedAt); err != nil {
		return StageRun{}, err
	}
	return run, nil
}

func (s *Store) projectColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(projects)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func requireProject(ctx context.Context, tx *sql.Tx, id string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrProjectNotFound
	}
	return nil
}

func latestStageStatus(ctx context.Context, tx *sql.Tx, projectID string, stage Stage) (StageStatus, error) {
	var status StageStatus
	err := tx.QueryRowContext(ctx, `SELECT status FROM stage_runs WHERE project_id = ? AND stage = ? ORDER BY created_at DESC LIMIT 1`, projectID, stage).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusNotStarted, nil
	}
	return status, err
}

func nextRevision(ctx context.Context, tx *sql.Tx, projectID string, stage Stage) int {
	var revision int
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(input_revision), 0) FROM stage_runs WHERE project_id = ? AND stage = ?`, projectID, stage).Scan(&revision)
	return revision + 1
}

func markDownstreamStale(ctx context.Context, tx *sql.Tx, projectID string, stage Stage) error {
	position := stagePosition(stage)
	if position < 0 {
		return ErrInvalidStage
	}
	for _, downstream := range OrderedStages[position+1:] {
		if _, err := tx.ExecContext(ctx, `UPDATE stage_runs SET status = ?, message_key = ?, updated_at = ? WHERE project_id = ? AND stage = ? AND status IN (?, ?, ?)`, StatusStale, "stage.stale", timestamp(time.Now().UTC()), projectID, downstream, StatusRunning, StatusReviewNeeded, StatusApproved); err != nil {
			return err
		}
	}
	return nil
}

func knownStage(stage Stage) bool { return stagePosition(stage) >= 0 }

func stagePosition(stage Stage) int {
	for index, item := range OrderedStages {
		if item == stage {
			return index
		}
	}
	return -1
}

func previousStage(stage Stage) Stage {
	position := stagePosition(stage)
	if position <= 0 {
		return ""
	}
	return OrderedStages[position-1]
}

func safeArtifactPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	return cleaned != "." && !strings.HasPrefix(cleaned, "../") && cleaned != ".."
}

func timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTimestamp(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

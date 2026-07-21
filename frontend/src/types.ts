export type Locale = 'vi' | 'en'

export type StageId = 'source' | 'translation' | 'dubbing_audio' | 'render' | 'outputs'
export type PersistentStageId = StageId
export type StageStatus = 'not_started' | 'queued' | 'running' | 'review_required' | 'approved' | 'stale' | 'failed' | 'cancelled'

export interface DesktopStage {
  id: StageId
  number: string
  title_vi: string
  title_en: string
}

export interface DesktopBootstrap {
  name: string
  legacy_api_base_url: string
  colab_notebook_url: string
  stages: DesktopStage[]
  locales: Locale[]
}

export interface VoiceHealth {
  reachable: boolean
  status: number
  data?: string
  message: string
}

export interface VoiceProfile {
  id: string
  name: string
  language: string
  status: string
}

export interface TTSOption {
  id: string
  label_vi: string
  label_en: string
  provider: string
  model: string
  needs_worker: boolean
  needs_profile: boolean
}

export interface TranslationModelOption {
	id: string
	label_vi: string
	label_en: string
}

export interface STTOption {
	id: string
	label_vi: string
	label_en: string
	provider: string
	model: string
}

export interface Project {
  id: string
  name: string
  target_language: string
  workflow_task_id?: string
  created_at: string
  updated_at: string
}

export interface StageRun {
  id: string
  project_id: string
  stage: PersistentStageId
  status: StageStatus
  input_revision: number
  message_key: string
  failure_code?: string
  created_at: string
  updated_at: string
}

export interface Artifact {
  id: string
  project_id: string
  stage_run_id: string
  kind: string
  path: string
  checksum: string
  revision: number
  created_at: string
}

export interface ProjectSnapshot {
  project: Project
  stage_runs: StageRun[]
  artifacts: Artifact[]
}

export interface DesktopWorkflowAction {
  run: StageRun
  workflow_task_id?: string
  message?: string
}

export interface WorkflowArtifact {
  kind: string
  label: string
  name: string
  download_url: string
}

export interface WorkflowProgressStep {
  id: string
  state: 'pending' | 'running' | 'completed' | 'failed'
  percent: number
  detail?: string
}

export interface DesktopWorkflowSnapshot {
  workflow_task_id: string
  current_stage: string
  failed_stage?: string
  process_percent: number
  message: string
  failure_reason?: string
  review_required: boolean
  source_srt_url?: string
	translated_srt_url?: string
	source_steps?: WorkflowProgressStep[]
  artifacts?: WorkflowArtifact[]
}

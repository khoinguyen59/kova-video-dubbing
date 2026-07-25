export type Locale = "vi" | "en";

export type StageId =
  "source" | "translation" | "dubbing_audio" | "render" | "outputs";
export type PersistentStageId = StageId;
export type StageStatus =
  | "not_started"
  | "queued"
  | "running"
  | "review_required"
  | "approved"
  | "stale"
  | "failed"
  | "cancelled";

export interface DesktopStage {
  id: StageId;
  number: string;
  title_vi: string;
  title_en: string;
}

export interface DesktopBootstrap {
  name: string;
  legacy_api_base_url: string;
  colab_notebook_url: string;
  stt_colab_notebook_url: string;
	ocr_colab_notebook_url: string;
  stages: DesktopStage[];
  locales: Locale[];
}

export interface VoiceHealth {
  reachable: boolean;
  status: number;
  data?: string;
  message: string;
}

export interface VoiceProfile {
  id: string;
  name: string;
  language: string;
  status: string;
  saved?: boolean;
  backup_available?: boolean;
  worker_url?: string;
	 reference_clean?: boolean;
}

export interface TTSOption {
  id: string;
  label_vi: string;
  label_en: string;
  provider: string;
  model: string;
  needs_worker: boolean;
  needs_profile: boolean;
}

export interface TranslationModelOption {
  id: string;
  label_vi: string;
  label_en: string;
}

export interface STTOption {
  id: string;
  label_vi: string;
  label_en: string;
  provider: string;
  model: string;
  needs_worker: boolean;
}

export interface Project {
  id: string;
  name: string;
  target_language: string;
  workflow_task_id?: string;
  created_at: string;
  updated_at: string;
}

export interface StageRun {
  id: string;
  project_id: string;
  stage: PersistentStageId;
  status: StageStatus;
  input_revision: number;
  message_key: string;
  failure_code?: string;
  created_at: string;
  updated_at: string;
}

export interface Artifact {
  id: string;
  project_id: string;
  stage_run_id: string;
  kind: string;
  path: string;
  checksum: string;
  revision: number;
  created_at: string;
}

export interface ProjectSnapshot {
  project: Project;
  stage_runs: StageRun[];
  artifacts: Artifact[];
}

export interface DesktopWorkflowAction {
  run: StageRun;
  workflow_task_id?: string;
  message?: string;
}

export interface WorkflowArtifact {
  kind: string;
  label: string;
  name: string;
  download_url: string;
}

export interface WorkflowProgressStep {
  id: string;
  state: "pending" | "running" | "completed" | "failed";
  percent: number;
  detail?: string;
}

export interface TranslationWarning {
  cue_index: number;
  suspicious_words: string[];
  reason?: "model_empty";
  text: string;
}

export interface DesktopFinalVideo {
  name: string;
  path: string;
  download_url: string;
  size_bytes: number;
}

export interface DesktopWorkflowSnapshot {
  workflow_task_id: string;
	review_mode?: "manual" | "auto";
  current_stage: string;
  failed_stage?: string;
  process_percent: number;
  message: string;
  failure_reason?: string;
  source_warning?: string;
  review_required: boolean;
  source_srt_url?: string;
  translated_srt_url?: string;
  source_steps?: WorkflowProgressStep[];
	translation_steps?: WorkflowProgressStep[];
  dubbing_steps?: WorkflowProgressStep[];
	 render_steps?: WorkflowProgressStep[];
  translation_warnings?: TranslationWarning[];
  updated_at?: string;
  estimated_completion_at?: string;
  completed_at?: string;
	final_video?: DesktopFinalVideo;
  artifacts?: WorkflowArtifact[];
}

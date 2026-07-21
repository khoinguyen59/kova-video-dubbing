import type {
  Artifact,
  DesktopWorkflowAction,
  DesktopWorkflowSnapshot,
  DesktopBootstrap,
  Project,
  ProjectSnapshot,
	StageRun,
	STTOption,
	TTSOption,
  TranslationModelOption,
  VoiceHealth,
  VoiceProfile,
} from './types'

type VoiceRequest = { base_url: string; token: string }
type ProjectRequest = { name: string; target_language: string }
export type WorkflowStageRequest = {
  project_id: string
  stage: string
  source_url: string
  translation_model_id: string
	tts_option_id: string
	stt_option_id: string
	stt_worker_url: string
	stt_worker_token: string
	source_method: 'speech_to_text' | 'visual_ocr'
	ocr_language: string
	ocr_region_x: number
	ocr_region_y: number
	ocr_region_width: number
	ocr_region_height: number
	ocr_sample_interval_ms: number
	ocr_prefer_gpu: boolean
	voice_profile_id: string
  worker_url: string
  worker_token: string
}

declare global {
  interface Window {
    __kovaStartupError?: (message: unknown) => void
    go?: {
      main?: {
        App?: {
          Bootstrap: () => Promise<DesktopBootstrap>
          OpenColabNotebook: (url: string) => Promise<void>
		  CheckVoiceHealth: (request: VoiceRequest) => Promise<VoiceHealth>
		  CheckSTTHealth: (request: VoiceRequest) => Promise<VoiceHealth>
          ListVoiceProfiles: (request: VoiceRequest) => Promise<VoiceProfile[]>
          ListTTSOptions: () => Promise<TTSOption[]>
		  ListSTTOptions: () => Promise<STTOption[]>
          ListTranslationModels: () => Promise<TranslationModelOption[]>
          CreateDesktopProject: (request: ProjectRequest) => Promise<Project>
          ListDesktopProjects: () => Promise<Project[]>
          GetDesktopProject: (projectId: string) => Promise<ProjectSnapshot>
          StartDesktopStage: (projectId: string, stage: string) => Promise<StageRun>
          MarkDesktopStageForReview: (runId: string, messageKey: string) => Promise<StageRun>
          ApproveDesktopStage: (runId: string) => Promise<StageRun>
          SaveDesktopDraft: (projectId: string, runId: string, stage: string, content: string) => Promise<Artifact>
          StartDesktopWorkflowStage: (request: WorkflowStageRequest) => Promise<DesktopWorkflowAction>
          RefreshDesktopWorkflow: (projectId: string) => Promise<DesktopWorkflowSnapshot>
          ReadDesktopWorkflowSubtitle: (projectId: string, stage: string) => Promise<string>
          SaveDesktopWorkflowDraft: (projectId: string, runId: string, stage: string, content: string) => Promise<Artifact>
          ApproveDesktopWorkflowStage: (projectId: string, runId: string, stage: string) => Promise<StageRun>
        }
      }
    }
  }
}

const fallbackBootstrap: DesktopBootstrap = {
  name: 'KOVA',
  legacy_api_base_url: 'http://127.0.0.1:8888',
  colab_notebook_url: 'https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb',
	stt_colab_notebook_url: 'https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/notebooks/KOVA_STT_GPU.ipynb',
  locales: ['vi', 'en'],
  stages: [
    { id: 'source', number: '01', title_vi: 'Nguồn video', title_en: 'Video source' },
    { id: 'translation', number: '02', title_vi: 'Dịch và phụ đề', title_en: 'Translation and subtitles' },
    { id: 'dubbing_audio', number: '03', title_vi: 'Giọng lồng tiếng cố định', title_en: 'Fixed dubbing voice' },
    { id: 'render', number: '04', title_vi: 'Xuất hình và tinh chỉnh', title_en: 'Video output and tuning' },
    { id: 'outputs', number: '05', title_vi: 'Chạy và nhận output', title_en: 'Run and receive outputs' },
  ],
}

function asArray<T>(value: unknown): T[] {
  return Array.isArray(value) ? value as T[] : []
}

function asText(value: unknown, fallback: string): string {
  return typeof value === 'string' && value.trim() ? value : fallback
}

// Wails is an external boundary. A bridge/runtime mismatch may produce null
// slices even when Go returns a non-nil slice; keep the desktop shell usable.
function normalizeBootstrap(value: unknown): DesktopBootstrap {
  if (!value || typeof value !== 'object') return fallbackBootstrap
  const source = value as Partial<DesktopBootstrap>
  const locales = asArray<DesktopBootstrap['locales'][number]>(source.locales)
  const stages = asArray<DesktopBootstrap['stages'][number]>(source.stages)
  return {
    name: asText(source.name, fallbackBootstrap.name),
	legacy_api_base_url: asText(source.legacy_api_base_url, fallbackBootstrap.legacy_api_base_url),
	colab_notebook_url: asText(source.colab_notebook_url, fallbackBootstrap.colab_notebook_url),
	stt_colab_notebook_url: asText(source.stt_colab_notebook_url, fallbackBootstrap.stt_colab_notebook_url),
    locales: locales.length ? locales : fallbackBootstrap.locales,
    stages: stages.length ? stages : fallbackBootstrap.stages,
  }
}

function desktopApp() {
  const app = window.go?.main?.App
  if (!app) throw new Error('Desktop binding is unavailable in browser preview.')
  return app
}

export async function bootstrap(): Promise<DesktopBootstrap> {
  const invoke = window.go?.main?.App?.Bootstrap
  if (!invoke) return fallbackBootstrap
  try {
    return normalizeBootstrap(await invoke())
  } catch {
    return fallbackBootstrap
  }
}

export async function openColabNotebook(url: string): Promise<void> {
  if (window.go?.main?.App?.OpenColabNotebook) return window.go.main.App.OpenColabNotebook(url)
  window.open(url, '_blank', 'noopener,noreferrer')
}

export async function checkVoiceHealth(baseUrl: string, token: string): Promise<VoiceHealth> {
  if (!window.go?.main?.App?.CheckVoiceHealth) return { reachable: false, status: 0, message: 'Desktop binding is unavailable in browser preview.' }
  const result = await window.go.main.App.CheckVoiceHealth({ base_url: baseUrl, token })
  return result && typeof result === 'object' ? result : { reachable: false, status: 0, message: 'Voice Studio returned no health response.' }
}

export async function checkSTTHealth(baseUrl: string, token: string): Promise<VoiceHealth> {
	if (!window.go?.main?.App?.CheckSTTHealth) return { reachable: false, status: 0, message: 'Desktop binding is unavailable in browser preview.' }
	const result = await window.go.main.App.CheckSTTHealth({ base_url: baseUrl, token })
	return result && typeof result === 'object' ? result : { reachable: false, status: 0, message: 'Colab STT worker returned no health response.' }
}

export async function listTTSOptions(): Promise<TTSOption[]> { return asArray<TTSOption>(await desktopApp().ListTTSOptions()) }
export async function listSTTOptions(): Promise<STTOption[]> { return asArray<STTOption>(await desktopApp().ListSTTOptions()) }
export async function listTranslationModels(): Promise<TranslationModelOption[]> { return asArray<TranslationModelOption>(await desktopApp().ListTranslationModels()) }
export async function listVoiceProfiles(baseUrl: string, token: string): Promise<VoiceProfile[]> { return asArray<VoiceProfile>(await desktopApp().ListVoiceProfiles({ base_url: baseUrl, token })) }
export function createDesktopProject(name: string, targetLanguage: string): Promise<Project> { return desktopApp().CreateDesktopProject({ name, target_language: targetLanguage }) }
export async function listDesktopProjects(): Promise<Project[]> { return asArray<Project>(await desktopApp().ListDesktopProjects()) }
export function getDesktopProject(projectId: string): Promise<ProjectSnapshot> { return desktopApp().GetDesktopProject(projectId) }
export function startDesktopStage(projectId: string, stage: string): Promise<StageRun> { return desktopApp().StartDesktopStage(projectId, stage) }
export function markDesktopStageForReview(runId: string, messageKey: string): Promise<StageRun> { return desktopApp().MarkDesktopStageForReview(runId, messageKey) }
export function approveDesktopStage(runId: string): Promise<StageRun> { return desktopApp().ApproveDesktopStage(runId) }
export function saveDesktopDraft(projectId: string, runId: string, stage: string, content: string): Promise<Artifact> { return desktopApp().SaveDesktopDraft(projectId, runId, stage, content) }
export function startDesktopWorkflowStage(request: WorkflowStageRequest): Promise<DesktopWorkflowAction> { return desktopApp().StartDesktopWorkflowStage(request) }
export function refreshDesktopWorkflow(projectId: string): Promise<DesktopWorkflowSnapshot> { return desktopApp().RefreshDesktopWorkflow(projectId) }
export function readDesktopWorkflowSubtitle(projectId: string, stage: string): Promise<string> { return desktopApp().ReadDesktopWorkflowSubtitle(projectId, stage) }
export function saveDesktopWorkflowDraft(projectId: string, runId: string, stage: string, content: string): Promise<Artifact> { return desktopApp().SaveDesktopWorkflowDraft(projectId, runId, stage, content) }
export function approveDesktopWorkflowStage(projectId: string, runId: string, stage: string): Promise<StageRun> { return desktopApp().ApproveDesktopWorkflowStage(projectId, runId, stage) }

import { useEffect, useMemo, useState } from 'react'
import {
  approveDesktopWorkflowStage,
  bootstrap,
  checkVoiceHealth,
  createDesktopProject,
  getDesktopProject,
  listDesktopProjects,
	listSTTOptions,
  listTTSOptions,
  listTranslationModels,
  listVoiceProfiles,
  markDesktopStageForReview,
  openColabNotebook,
  readDesktopWorkflowSubtitle,
  refreshDesktopWorkflow,
  saveDesktopWorkflowDraft,
  startDesktopWorkflowStage,
} from './api'
import { stageTitle, t } from './i18n'
import type { DesktopBootstrap, DesktopWorkflowSnapshot, Locale, PersistentStageId, Project, ProjectSnapshot, StageId, StageRun, StageStatus, STTOption, TTSOption, TranslationModelOption, VoiceProfile, WorkflowProgressStep } from './types'

const emptyBootstrap: DesktopBootstrap = { name: 'KOVA', legacy_api_base_url: '', colab_notebook_url: '', locales: ['vi', 'en'], stages: [] }
const initialStatuses: Record<StageId, StageStatus> = { source: 'not_started', translation: 'not_started', dubbing_audio: 'not_started', render: 'not_started', outputs: 'not_started' }

function hintKey(stage: StageId): 'sourceHint' | 'translationHint' | 'dubbingHint' | 'renderHint' | 'outputsHint' {
  const hints: Record<StageId, 'sourceHint' | 'translationHint' | 'dubbingHint' | 'renderHint' | 'outputsHint'> = {
    source: 'sourceHint', translation: 'translationHint', dubbing_audio: 'dubbingHint', render: 'renderHint', outputs: 'outputsHint',
  }
  return hints[stage]
}

function statusLabel(locale: Locale, status: StageStatus): string {
  const labels: Record<Locale, Record<StageStatus, string>> = {
    vi: { not_started: 'Chưa chạy', queued: 'Đang chờ', running: 'Đang thực hiện', review_required: 'Cần kiểm tra', approved: 'Đã duyệt', stale: 'Cần chạy lại', failed: 'Có lỗi', cancelled: 'Đã hủy' },
    en: { not_started: 'Not started', queued: 'Queued', running: 'In progress', review_required: 'Needs review', approved: 'Approved', stale: 'Run again', failed: 'Failed', cancelled: 'Cancelled' },
  }
  return labels[locale][status]
}

function persistentStage(_stage: StageId): _stage is PersistentStageId { return true }

function previousStage(stage: StageId): PersistentStageId | null {
  const prerequisites: Record<StageId, PersistentStageId | null> = {
    source: null, translation: 'source', dubbing_audio: 'translation', render: 'dubbing_audio', outputs: 'render',
  }
  return prerequisites[stage]
}

function latestRun(snapshot: ProjectSnapshot | null, stage: PersistentStageId): StageRun | undefined {
  return Array.isArray(snapshot?.stage_runs) ? snapshot.stage_runs.filter((run) => run.stage === stage).at(-1) : undefined
}

function formatRunTime(locale: Locale, value?: string): string {
  if (!value) return '—'
  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) return value
  return new Intl.DateTimeFormat(locale === 'vi' ? 'vi-VN' : 'en-GB', { dateStyle: 'short', timeStyle: 'medium' }).format(timestamp)
}

function formatElapsed(locale: Locale, startedAt: string | undefined, now: number): string {
  const started = startedAt ? Date.parse(startedAt) : Number.NaN
  if (Number.isNaN(started)) return '—'
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainder = seconds % 60
  if (locale === 'vi') return hours ? `${hours} giờ ${minutes} phút ${remainder} giây` : `${minutes} phút ${remainder} giây`
  return hours ? `${hours}h ${minutes}m ${remainder}s` : `${minutes}m ${remainder}s`
}

function safePercent(value: number | undefined): number {
  return Math.max(0, Math.min(100, Number.isFinite(value) ? Number(value) : 0))
}

function sourceStepTitle(locale: Locale, id: string): string {
  const titles: Record<string, Record<Locale, string>> = {
    download_video: { vi: 'Tải video nguồn', en: 'Download source video' },
    download_audio: { vi: 'Tải audio nguồn', en: 'Download source audio' },
    speech_to_text: { vi: 'Speech-to-text (tạo transcript)', en: 'Speech-to-text (create transcript)' },
    source_srt: { vi: 'Tạo SRT gốc để kiểm tra', en: 'Create source SRT for review' },
  }
  return titles[id]?.[locale] ?? id
}

function sourceStepStateLabel(locale: Locale, state: WorkflowProgressStep['state']): string {
  const labels: Record<WorkflowProgressStep['state'], Record<Locale, string>> = {
    pending: { vi: 'Chờ', en: 'Pending' },
    running: { vi: 'Đang thực hiện', en: 'In progress' },
    completed: { vi: 'Hoàn tất', en: 'Completed' },
    failed: { vi: 'Có lỗi', en: 'Failed' },
  }
  return labels[state]?.[locale] ?? state
}

function workflowArtifactURL(apiBaseURL: string, downloadURL: string): string {
  if (!downloadURL) return '#'
  try { return new URL(downloadURL, apiBaseURL).toString() } catch { return '#' }
}

function formatMediaTime(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '00:00'
  const total = Math.floor(value)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  return hours ? `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}` : `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`
}

function statusesFor(snapshot: ProjectSnapshot | null): Record<StageId, StageStatus> {
  if (!snapshot) return initialStatuses
  return {
    source: latestRun(snapshot, 'source')?.status ?? 'not_started',
    translation: latestRun(snapshot, 'translation')?.status ?? 'not_started',
    dubbing_audio: latestRun(snapshot, 'dubbing_audio')?.status ?? 'not_started',
    render: latestRun(snapshot, 'render')?.status ?? 'not_started',
    outputs: latestRun(snapshot, 'outputs')?.status ?? 'not_started',
  }
}

export default function App() {
  const [data, setData] = useState<DesktopBootstrap>(emptyBootstrap)
  const [locale, setLocale] = useState<Locale>('vi')
  const [activeStage, setActiveStage] = useState<StageId>('source')
  const [projects, setProjects] = useState<Project[]>([])
  const [snapshot, setSnapshot] = useState<ProjectSnapshot | null>(null)
  const [projectName, setProjectName] = useState('')
  const [draft, setDraft] = useState('')
	const [loadedDraftKey, setLoadedDraftKey] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [workerUrl, setWorkerUrl] = useState('')
  const [workerToken, setWorkerToken] = useState('')
  const [connectionMessage, setConnectionMessage] = useState('')
  const [ttsOptions, setTTSOptions] = useState<TTSOption[]>([])
  const [ttsOptionID, setTTSOptionID] = useState('omnivoice')
	const [sttOptions, setSTTOptions] = useState<STTOption[]>([])
	const [sttOptionID, setSTTOptionID] = useState('fasterwhisper-medium')
  const [translationModels, setTranslationModels] = useState<TranslationModelOption[]>([])
  const [translationModelID, setTranslationModelID] = useState('oc/deepseek-v4-flash-free')
  const [voiceProfiles, setVoiceProfiles] = useState<VoiceProfile[]>([])
  const [voiceProfileID, setVoiceProfileID] = useState('')
  const [workflowStatus, setWorkflowStatus] = useState<DesktopWorkflowSnapshot | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const [previewTime, setPreviewTime] = useState(0)
  const [previewDuration, setPreviewDuration] = useState(0)
  const [previewError, setPreviewError] = useState('')

  const stage = useMemo(() => data.stages.find((item) => item.id === activeStage), [activeStage, data.stages])
  const statuses = useMemo(() => statusesFor(snapshot), [snapshot])
  const activeRun = persistentStage(activeStage) ? latestRun(snapshot, activeStage) : undefined
  const selectedTTS = ttsOptions.find((option) => option.id === ttsOptionID)
  const prerequisite = previousStage(activeStage)
  const canStart = Boolean(snapshot && activeRun?.status !== 'running' && (!prerequisite || statuses[prerequisite] === 'approved') && (activeStage !== 'source' || draft.trim()))

  useEffect(() => {
    void (async () => {
      try {
		const [nextBootstrap, nextOptions, nextSTTOptions, nextTranslationModels, nextProjects] = await Promise.all([bootstrap(), listTTSOptions(), listSTTOptions(), listTranslationModels(), listDesktopProjects()])
        setData(nextBootstrap)
        setTTSOptions(nextOptions)
		setSTTOptions(nextSTTOptions)
		if (nextSTTOptions.some((option) => option.id === 'fasterwhisper-medium')) setSTTOptionID('fasterwhisper-medium')
        setTranslationModels(nextTranslationModels)
        if (nextTranslationModels[0]) setTranslationModelID(nextTranslationModels[0].id)
        setProjects(nextProjects)
        if (nextProjects[0]) setSnapshot(await getDesktopProject(nextProjects[0].id))
      } catch (error) {
        setMessage(`${t('vi', 'actionFailed')} ${asMessage(error)}`)
        setData(await bootstrap())
      }
    })()
  }, [])

  useEffect(() => {
    const projectID = snapshot?.project.id
    const workflowTaskID = snapshot?.project.workflow_task_id
    if (!projectID || !workflowTaskID || workflowStatus?.workflow_task_id === workflowTaskID) return
    let cancelled = false
    void refreshDesktopWorkflow(projectID)
      .then((nextWorkflow) => { if (!cancelled) setWorkflowStatus(nextWorkflow) })
      .catch((error) => { if (!cancelled) setMessage(`${locale === 'vi' ? 'Không thể tải trạng thái/artifact của worker:' : 'Could not load worker status/artifacts:'} ${asMessage(error)}`) })
    return () => { cancelled = true }
  }, [locale, snapshot?.project.id, snapshot?.project.workflow_task_id, workflowStatus?.workflow_task_id])

	useEffect(() => {
		const projectID = snapshot?.project.id
		const subtitleURL = activeStage === 'source' ? workflowStatus?.source_srt_url : activeStage === 'translation' ? workflowStatus?.translated_srt_url : ''
		if (!projectID || !subtitleURL || (activeStage !== 'source' && activeStage !== 'translation')) return
		const key = `${projectID}:${activeStage}:${subtitleURL}`
		if (loadedDraftKey === key) return
		let cancelled = false
		void readDesktopWorkflowSubtitle(projectID, activeStage)
			.then((content) => {
				if (cancelled) return
				setDraft(content)
				setLoadedDraftKey(key)
			})
			.catch((error) => { if (!cancelled) setMessage(`${locale === 'vi' ? 'Không thể mở SRT để kiểm tra:' : 'Could not open the review SRT:'} ${asMessage(error)}`) })
		return () => { cancelled = true }
	}, [activeStage, loadedDraftKey, locale, snapshot?.project.id, workflowStatus?.source_srt_url, workflowStatus?.translated_srt_url])

  useEffect(() => {
    if (activeRun?.status !== 'running') return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [activeRun?.id, activeRun?.status])

  useEffect(() => {
    const projectID = snapshot?.project.id
    const workflowTaskID = snapshot?.project.workflow_task_id
    if (!projectID || !workflowTaskID || activeRun?.status !== 'running') return
    let cancelled = false
    const poll = async () => {
      try {
        const nextWorkflow = await refreshDesktopWorkflow(projectID)
        if (cancelled) return
        setWorkflowStatus(nextWorkflow)
        setMessage(nextWorkflow.failure_reason ? `${locale === 'vi' ? 'Lỗi worker:' : 'Worker error:'} ${nextWorkflow.failure_reason}` : '')
        setSnapshot(await getDesktopProject(projectID))
      } catch (error) {
        if (!cancelled) setMessage(`${locale === 'vi' ? 'Không thể cập nhật trạng thái worker:' : 'Could not refresh worker status:'} ${asMessage(error)}`)
      }
    }
    void poll()
    const timer = window.setInterval(() => void poll(), 4000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [activeRun?.id, activeRun?.status, locale, snapshot?.project.id, snapshot?.project.workflow_task_id])

  async function refreshProjects(selectID?: string) {
    const nextProjects = await listDesktopProjects()
    setProjects(nextProjects)
    const id = selectID ?? snapshot?.project.id ?? nextProjects[0]?.id
    if (id) setSnapshot(await getDesktopProject(id))
  }

  async function handleCreateProject() {
    if (!projectName.trim()) return
    await withBusy(async () => {
      const created = await createDesktopProject(projectName.trim(), 'vi')
      setProjectName('')
		setWorkflowStatus(null)
		setLoadedDraftKey('')
      await refreshProjects(created.id)
      setMessage('')
    })
  }

  async function handleSelectProject(projectID: string) {
    await withBusy(async () => {
      setSnapshot(await getDesktopProject(projectID))
		setWorkflowStatus(null)
		setLoadedDraftKey('')
      setMessage('')
    })
  }

  async function handleStartStage() {
    if (!snapshot) return
    const projectID = snapshot.project.id
    setBusy(true)
    try {
      const action = await startDesktopWorkflowStage({
        project_id: snapshot.project.id,
        stage: activeStage,
        source_url: activeStage === 'source' ? draft.trim() : '',
		stt_option_id: sttOptionID,
        translation_model_id: translationModelID,
        tts_option_id: ttsOptionID,
        voice_profile_id: voiceProfileID,
        worker_url: workerUrl,
        worker_token: workerToken,
      })
      await refreshProjects(projectID)
      setWorkflowStatus({
        workflow_task_id: action.workflow_task_id ?? '',
        current_stage: 'starting',
        process_percent: 0,
        message: locale === 'vi' ? 'Đã gửi yêu cầu. KOVA đang chờ worker nhận job.' : 'Request sent. KOVA is waiting for the worker to accept the job.',
        review_required: false,
      })
      setMessage('')
    } catch (error) {
      try { await refreshProjects(projectID) } catch { /* Preserve the actionable start error below. */ }
      setMessage(`${t(locale, 'actionFailed')} ${asMessage(error)}`)
    } finally {
      setBusy(false)
    }
  }

  async function handleSaveDraft() {
    if (!snapshot || !activeRun || !draft.trim()) return
    await withBusy(async () => {
      await saveDesktopWorkflowDraft(snapshot.project.id, activeRun.id, activeStage, draft)
      if (snapshot.project.workflow_task_id) setWorkflowStatus(await refreshDesktopWorkflow(snapshot.project.id))
      await refreshProjects(snapshot.project.id)
    })
  }

  async function handleMarkForReview() {
    if (!snapshot || !activeRun) return
    await withBusy(async () => {
      await markDesktopStageForReview(activeRun.id, 'stage.review_required')
      await refreshProjects(snapshot.project.id)
    })
  }

  async function handleApprove() {
    if (!snapshot || !activeRun) return
    await withBusy(async () => {
      await approveDesktopWorkflowStage(snapshot.project.id, activeRun.id, activeStage)
      await refreshProjects(snapshot.project.id)
    })
  }

  async function handleRefreshWorkflow() {
    if (!snapshot) return
    await withBusy(async () => {
      const workflow = await refreshDesktopWorkflow(snapshot.project.id)
      setWorkflowStatus(workflow)
      setMessage(workflow.failure_reason ? `${locale === 'vi' ? 'Lỗi worker:' : 'Worker error:'} ${workflow.failure_reason}` : '')
      await refreshProjects(snapshot.project.id)
    })
  }

  async function handleConnectionCheck() {
    const result = await checkVoiceHealth(workerUrl, workerToken)
    setConnectionMessage(result.reachable ? t(locale, 'connectionSuccess') : `${t(locale, 'connectionFailed')} ${result.message}`)
  }

  async function handleLoadProfiles() {
    await withBusy(async () => {
      const profiles = await listVoiceProfiles(workerUrl, workerToken)
      setVoiceProfiles(profiles)
      if (!voiceProfileID && profiles[0]) setVoiceProfileID(profiles[0].id)
      setConnectionMessage(t(locale, 'profilesLoaded'))
    })
  }

  async function withBusy(action: () => Promise<void>) {
    setBusy(true)
    try {
      await action()
    } catch (error) {
      setMessage(`${t(locale, 'actionFailed')} ${asMessage(error)}`)
    } finally {
      setBusy(false)
    }
  }

  const failureDetail = workflowStatus?.failure_reason || activeRun?.failure_code || ''
  const workflowPercent = safePercent(workflowStatus?.process_percent)
  const sourceSteps = activeStage === 'source' && Array.isArray(workflowStatus?.source_steps) ? workflowStatus.source_steps : []
  const sourceSRTAvailable = Boolean(workflowStatus?.source_srt_url)
  const workflowArtifacts = Array.isArray(workflowStatus?.artifacts) ? workflowStatus.artifacts : []
  const previewArtifact = workflowArtifacts.find((artifact) => artifact.kind === 'subtitled_horizontal_video') ?? workflowArtifacts.find((artifact) => artifact.kind === 'dubbed_video') ?? workflowArtifacts.find((artifact) => artifact.kind === 'source_video')
  const previewURL = previewArtifact ? workflowArtifactURL(data.legacy_api_base_url, previewArtifact.download_url) : ''
  const previewPercent = previewDuration > 0 ? safePercent((previewTime / previewDuration) * 100) : 0
  useEffect(() => { setPreviewTime(0); setPreviewDuration(0); setPreviewError('') }, [previewURL])
  const stageMessage = activeRun?.status === 'running'
    ? (workflowStatus?.message || t(locale, 'stageRunning'))
    : activeRun?.status === 'review_required'
      ? t(locale, 'stageReview')
      : activeRun?.status === 'approved'
        ? t(locale, 'stageApproved')
        : activeRun?.status === 'failed'
          ? (locale === 'vi' ? 'Bước thất bại. Xem lỗi chi tiết bên dưới trước khi chạy lại.' : 'The stage failed. Review the detailed error below before retrying.')
          : ''

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="brand"><span className="brand-mark">K</span><span>KOVA</span></div>
        <div className="project-name">{snapshot?.project.name ?? t(locale, 'newProject')}</div>
        <div className="topbar-actions">
          <span className="status-online">● {t(locale, 'ready')}</span>
          <label className="locale-select"><span>{t(locale, 'language')}</span><select value={locale} onChange={(event) => setLocale(event.target.value as Locale)}><option value="vi">Tiếng Việt</option><option value="en">English</option></select></label>
        </div>
      </header>

      <section className="workspace">
        <aside className="sidebar" aria-label="KOVA workflow">
          <p className="sidebar-title">KOVA WORKFLOW</p>
          <nav>
            {data.stages.map((item) => (
			  <button className={`stage-nav ${item.id === activeStage ? 'selected' : ''}`} key={item.id} onClick={() => { setActiveStage(item.id); setDraft(''); setLoadedDraftKey(''); setWorkflowStatus(null); setMessage('') }}>
                <span className={`stage-dot ${statuses[item.id]}`} />
                <span><strong>{item.number}</strong> · {stageTitle(locale, item)}</span>
                <small>{statusLabel(locale, statuses[item.id])}</small>
              </button>
            ))}
          </nav>
          <div className="sidebar-bottom"><button className="quiet-button">⚙ {t(locale, 'settings')}</button><button className="quiet-button">◷ {t(locale, 'history')}</button></div>
        </aside>

        <section className="center-pane">
          <div className="project-toolbar">
            <label>{t(locale, 'selectProject')}<select value={snapshot?.project.id ?? ''} onChange={(event) => void handleSelectProject(event.target.value)}><option value="" disabled>{t(locale, 'noProject')}</option>{projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}</select></label>
            <label>{t(locale, 'newProjectName')}<input value={projectName} onChange={(event) => setProjectName(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void handleCreateProject() }} /></label>
            <button className="secondary" disabled={busy || !projectName.trim()} onClick={() => void handleCreateProject()}>{t(locale, 'createProject')}</button>
          </div>
          <div className="breadcrumb">{stage?.number} · {stage ? stageTitle(locale, stage) : ''}</div>
          <div className="preview-card">
            {previewURL ? (
              <div className="preview-media">
                <video className="preview-video" key={previewURL} src={previewURL} controls preload="metadata" onLoadedMetadata={(event) => { setPreviewDuration(event.currentTarget.duration); setPreviewTime(event.currentTarget.currentTime); setPreviewError('') }} onTimeUpdate={(event) => setPreviewTime(event.currentTarget.currentTime)} onError={() => setPreviewError(locale === 'vi' ? 'Không thể phát video nguồn. Hãy mở artifact từ worker để tải file hoặc kiểm tra lại worker.' : 'The source video could not be played. Open the worker artifact to download it or check the worker.')} />
                {previewError && <p className="preview-error">{previewError}</p>}
              </div>
            ) : (
              <div className="preview-placeholder"><span>▶</span><p>{snapshot ? (locale === 'vi' ? 'Chưa có video từ worker. Video sẽ hiện ở đây ngay khi artifact nguồn được tạo.' : 'No worker video yet. The source artifact will appear here as soon as it is created.') : t(locale, 'noProject')}</p></div>
            )}
            <div className="timeline"><span>{formatMediaTime(previewTime)}</span><div className="timeline-line"><i style={{ width: `${previewPercent}%` }} /></div><span>{formatMediaTime(previewDuration)}</span></div>
          </div>
          <section className="stage-card">
            <h1>{stage ? stageTitle(locale, stage) : data.name}</h1>
            <p>{t(locale, hintKey(activeStage))}</p>
            {activeRun && (
              <section className={`workflow-status ${activeRun.status}`} aria-live="polite">
                <div className="workflow-status-heading">
                  <strong>{locale === 'vi' ? 'Theo dõi tác vụ' : 'Task tracking'}</strong>
                  <span>{statusLabel(locale, activeRun.status)}</span>
                </div>
                <div className="workflow-progress-label"><span>{activeStage === 'source' ? (locale === 'vi' ? 'Tổng tiến độ nguồn' : 'Overall source progress') : (locale === 'vi' ? 'Tiến độ worker' : 'Worker progress')}</span><strong>{workflowPercent}%</strong></div>
                <div className="workflow-progress-track" role="progressbar" aria-label={activeStage === 'source' ? (locale === 'vi' ? 'Tổng tiến độ nguồn' : 'Overall source progress') : (locale === 'vi' ? 'Tiến độ worker' : 'Worker progress')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={workflowPercent}><i style={{ width: `${workflowPercent}%` }} /></div>
                {sourceSteps.length > 0 && <section className="source-step-list" aria-label={locale === 'vi' ? 'Các bước xử lý nguồn' : 'Source processing steps'}>
                  <h2>{locale === 'vi' ? 'Các bước xử lý nguồn' : 'Source processing steps'}</h2>
                  {sourceSteps.map((step) => {
                    const percent = step.state === 'completed' ? 100 : safePercent(step.percent)
                    return <div key={step.id} className={`source-step ${step.state}`}>
                      <div className="source-step-heading"><strong>{sourceStepTitle(locale, step.id)}</strong><span>{sourceStepStateLabel(locale, step.state)}</span></div>
                      <div className="source-step-progress-label"><span>{percent}%</span></div>
                      <div className="source-step-track" role="progressbar" aria-label={sourceStepTitle(locale, step.id)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><i style={{ width: `${percent}%` }} /></div>
                      {step.detail && <small>{step.detail}</small>}
                    </div>
                  })}
                </section>}
                <dl className="workflow-metadata">
                  <div><dt>{locale === 'vi' ? 'Bắt đầu' : 'Started'}</dt><dd>{formatRunTime(locale, activeRun.created_at)}</dd></div>
                  <div><dt>{locale === 'vi' ? 'Đã chạy' : 'Elapsed'}</dt><dd>{formatElapsed(locale, activeRun.created_at, now)}</dd></div>
                  <div><dt>{locale === 'vi' ? 'Cập nhật gần nhất' : 'Last update'}</dt><dd>{formatRunTime(locale, activeRun.updated_at)}</dd></div>
                  {snapshot?.project.workflow_task_id && <div><dt>{locale === 'vi' ? 'Mã job' : 'Job ID'}</dt><dd><code>{snapshot.project.workflow_task_id}</code></dd></div>}
                </dl>
                {activeRun.status === 'running' && <p className="workflow-poll-note">{locale === 'vi' ? 'Tự cập nhật mỗi 4 giây. Thời gian hoàn tất chỉ là ước lượng của worker nên KOVA hiển thị thời gian đã chạy và tiến độ thực tế.' : 'Updates automatically every 4 seconds. Completion time is worker-dependent, so KOVA shows actual elapsed time and progress.'}</p>}
                {workflowStatus?.message && <p className="workflow-worker-message">{workflowStatus.message}</p>}
                {failureDetail && <p className="workflow-failure"><strong>{locale === 'vi' ? 'Lỗi chi tiết:' : 'Detailed error:'}</strong> {failureDetail}</p>}
              </section>
            )}
            {activeStage === 'translation' && (
              <div className="worker-form">
                <label>{locale === 'vi' ? 'Model dịch miễn phí' : 'Free translation model'}<select value={translationModelID} onChange={(event) => setTranslationModelID(event.target.value)}>{translationModels.map((model) => <option key={model.id} value={model.id}>{locale === 'vi' ? model.label_vi : model.label_en}</option>)}</select></label>
                <p className="worker-help">{locale === 'vi' ? 'KOVA chỉ gửi các model free đã kiểm chứng qua API Gateway trong danh sách này.' : 'KOVA sends only the verified free gateway models listed here.'}</p>
              </div>
            )}
            {activeStage === 'dubbing_audio' && (
              <div className="worker-form">
                <label>{t(locale, 'ttsProvider')}<select value={ttsOptionID} onChange={(event) => setTTSOptionID(event.target.value)}>{ttsOptions.map((option) => <option key={option.id} value={option.id}>{locale === 'vi' ? option.label_vi : option.label_en}</option>)}</select></label>
                {selectedTTS?.needs_profile && <label>{t(locale, 'fixedVoice')}<select value={voiceProfileID} onChange={(event) => setVoiceProfileID(event.target.value)}><option value="">{t(locale, 'noProfile')}</option>{voiceProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.language}</option>)}</select></label>}
                {selectedTTS?.needs_worker && <><button className="secondary" onClick={() => void openColabNotebook(data.colab_notebook_url)}>{t(locale, 'openColab')}</button><label>{t(locale, 'colabUrl')}<input placeholder="https://xxxx.trycloudflare.com" value={workerUrl} onChange={(event) => setWorkerUrl(event.target.value)} /></label><label>{t(locale, 'colabToken')}<input type="password" autoComplete="off" value={workerToken} onChange={(event) => setWorkerToken(event.target.value)} /></label><button className="secondary" disabled={busy || !workerUrl.trim()} onClick={() => void handleConnectionCheck()}>{t(locale, 'checkConnection')}</button><button className="secondary" disabled={busy || !workerUrl.trim()} onClick={() => void handleLoadProfiles()}>{t(locale, 'loadProfiles')}</button></>}
                {selectedTTS?.needs_profile && <p className="worker-help">{t(locale, 'profileHelp')}</p>}
                {connectionMessage && <p className="connection-message">{connectionMessage}</p>}
              </div>
            )}
			{activeStage === 'source' && <div className="worker-form source-stt-form">
				<label>{locale === 'vi' ? 'Speech-to-text' : 'Speech-to-text'}<select value={sttOptionID} disabled={busy || activeRun?.status === 'running'} onChange={(event) => setSTTOptionID(event.target.value)}>{sttOptions.map((option) => <option key={option.id} value={option.id}>{locale === 'vi' ? option.label_vi : option.label_en}</option>)}</select></label>
				<p className="worker-help">{locale === 'vi' ? 'STT chạy bằng Faster-Whisper cục bộ. Lần đầu KOVA tải engine và model đã chọn từ nguồn phát hành công khai; không dùng API Gateway hay phụ đề YouTube.' : 'STT uses local Faster-Whisper. On first use, KOVA downloads the engine and selected model from its public release; it does not use an API Gateway or YouTube captions.'}</p>
			</div>}
            {activeStage === 'source' && activeRun?.status === 'running' && <p className="worker-help">{locale === 'vi' ? 'KOVA đang tải video/audio, sau đó chạy speech-to-text để tạo SRT gốc có timestamp. Không cần phụ đề YouTube.' : 'KOVA is downloading the video/audio, then running speech-to-text to create a timestamped source SRT. YouTube captions are not required.'}</p>}
            {persistentStage(activeStage) && snapshot && (activeRun || activeStage === 'source') && <label className="draft-editor">{activeStage === 'source' ? (sourceSRTAvailable ? (locale === 'vi' ? 'SRT gốc từ speech-to-text — kiểm tra và sửa' : 'Source SRT from speech-to-text — review and edit') : (locale === 'vi' ? 'URL nguồn — sửa rồi chạy lại' : 'Source URL — edit and retry')) : activeStage === 'translation' ? (locale === 'vi' ? 'SRT tiếng Việt — kiểm tra và sửa' : 'Vietnamese SRT — review and edit') : t(locale, 'edit')}<textarea value={draft} placeholder={activeStage === 'source' ? (sourceSRTAvailable ? (locale === 'vi' ? 'Kiểm tra và sửa SRT gốc trước khi duyệt.' : 'Review and edit the source SRT before approval.') : (locale === 'vi' ? 'Dán URL video nguồn trước khi chạy.' : 'Paste the source video URL before starting.')) : t(locale, 'draftPlaceholder')} onChange={(event) => setDraft(event.target.value)} disabled={activeRun?.status === 'running' || activeRun?.status === 'approved'} /></label>}
            {stageMessage && <p className="stage-message">{stageMessage}</p>}
            {message && <p className="error-message">{message}</p>}
          </section>
          <footer className="stage-actions">
            <button className="secondary" disabled={busy || !snapshot || !activeRun || activeRun.status !== 'review_required' || !draft.trim()} onClick={() => void handleSaveDraft()}>{t(locale, 'saveDraft')}</button>
            <button className="primary" disabled={busy || !canStart} onClick={() => void handleStartStage()}>{t(locale, 'start')}</button>
            <button className="secondary" disabled={busy || !snapshot?.project.workflow_task_id} onClick={() => void handleRefreshWorkflow()}>{t(locale, 'refreshWorkflow')}</button>
            <button className="secondary" disabled={busy || Boolean(snapshot?.project.workflow_task_id) || !activeRun || activeRun.status !== 'running'} onClick={() => void handleMarkForReview()}>{t(locale, 'sendForReview')}</button>
            <button className="success" disabled={busy || !activeRun || activeRun.status !== 'review_required' || !draft.trim()} onClick={() => void handleApprove()}>{t(locale, 'approve')}</button>
          </footer>
        </section>

        <aside className="right-pane">
          <div className="right-tabs"><button className="selected">✦ {t(locale, 'style')}</button><button>◎ {t(locale, 'review')}</button><button>⌁ {t(locale, 'ocr')}</button><button>◷ {t(locale, 'history')}</button></div>
          <section className="inspector-card"><h2>{t(locale, 'review')}</h2><p>{stageMessage || t(locale, 'noProject')}</p></section>
          <section className="inspector-card"><h2>{locale === 'vi' ? 'Artifact từ worker' : 'Worker artifacts'}</h2>{workflowArtifacts.length ? <ul className="artifact-list">{workflowArtifacts.map((artifact) => <li key={`${artifact.kind}-${artifact.download_url}`}><strong>{artifact.label || artifact.kind}</strong><a href={workflowArtifactURL(data.legacy_api_base_url, artifact.download_url)} target="_blank" rel="noreferrer">{artifact.name || artifact.download_url}</a></li>)}</ul> : <p>{locale === 'vi' ? 'Worker chưa trả artifact nào.' : 'The worker has not returned an artifact yet.'}</p>}</section>
          <section className="inspector-card"><h2>{t(locale, 'artifacts')}</h2>{Array.isArray(snapshot?.artifacts) && snapshot.artifacts.length ? <ul className="artifact-list">{snapshot.artifacts.slice().reverse().map((artifact) => <li key={artifact.id}><strong>{artifact.kind}</strong><span>{artifact.path}</span></li>)}</ul> : <p>{t(locale, 'noArtifacts')}</p>}</section>
        </aside>
      </section>
    </main>
  )
}

function asMessage(error: unknown): string { return error instanceof Error ? error.message : String(error) }

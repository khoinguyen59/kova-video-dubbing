import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import {
  approveDesktopWorkflowStage,
  bootstrap,
	checkOCRHealth,
  checkVoiceHealth,
	checkVisualOCR,
	createVoiceProfile,
  createDesktopProject,
	deleteVoiceProfile,
	deleteDesktopProject,
	getCapCutDraftSettings,
  getDesktopProject,
  listDesktopProjects,
  listSTTOptions,
  checkSTTHealth,
  listTTSOptions,
  listTranslationModels,
  listVoiceProfiles,
  markDesktopStageForReview,
  openColabNotebook,
  openShortVideoSession,
	previewVoiceProfile,
  readDesktopWorkflowSubtitle,
  refreshDesktopWorkflow,
	  revealDesktopWorkflowFinalVideo,
	  saveDesktopWorkflowFinalVideo,
	saveDesktopWorkflowDraft,
	selectSourceVideo,
	selectVoiceReferenceAudio,
	installVisualOCR,
	saveCapCutDraftSettings,
	selectCapCutDraftRoot,
	startDesktopWorkflowStage,
	type CapCutDraftSettings,
} from "./api";
import { stageTitle, t } from "./i18n";
import type {
  DesktopBootstrap,
  DesktopWorkflowSnapshot,
  Locale,
  PersistentStageId,
  Project,
  ProjectSnapshot,
  StageId,
  StageRun,
  StageStatus,
  STTOption,
  TTSOption,
	TranslationModelOption,
  VoiceProfile,
  WorkflowProgressStep,
} from "./types";

const emptyBootstrap: DesktopBootstrap = {
  name: "KOVA",
  legacy_api_base_url: "",
  colab_notebook_url: "",
  stt_colab_notebook_url: "",
	ocr_colab_notebook_url: "",
  locales: ["vi", "en"],
  stages: [],
};
const initialStatuses: Record<StageId, StageStatus> = {
  source: "not_started",
  translation: "not_started",
  dubbing_audio: "not_started",
  render: "not_started",
  outputs: "not_started",
};

const sourceLanguageOptions = [
  { value: "auto", vi: "Tự nhận diện", en: "Auto detect" },
  { value: "en", vi: "Tiếng Anh", en: "English" },
  { value: "vi", vi: "Tiếng Việt", en: "Vietnamese" },
  { value: "zh_cn", vi: "Tiếng Trung (giản thể)", en: "Chinese (Simplified)" },
  { value: "zh_tw", vi: "Tiếng Trung (phồn thể)", en: "Chinese (Traditional)" },
  { value: "ja", vi: "Tiếng Nhật", en: "Japanese" },
  { value: "ko", vi: "Tiếng Hàn", en: "Korean" },
  { value: "th", vi: "Tiếng Thái", en: "Thai" },
  { value: "id", vi: "Tiếng Indonesia", en: "Indonesian" },
  { value: "es", vi: "Tiếng Tây Ban Nha", en: "Spanish" },
  { value: "fr", vi: "Tiếng Pháp", en: "French" },
  { value: "de", vi: "Tiếng Đức", en: "German" },
  { value: "pt", vi: "Tiếng Bồ Đào Nha", en: "Portuguese" },
  { value: "ru", vi: "Tiếng Nga", en: "Russian" },
];

const targetLanguageOptions = sourceLanguageOptions.filter((language) => language.value !== "auto");

function hintKey(
  stage: StageId,
):
  | "sourceHint"
  | "translationHint"
  | "dubbingHint"
  | "renderHint"
  | "outputsHint" {
  const hints: Record<
    StageId,
    | "sourceHint"
    | "translationHint"
    | "dubbingHint"
    | "renderHint"
    | "outputsHint"
  > = {
    source: "sourceHint",
    translation: "translationHint",
    dubbing_audio: "dubbingHint",
    render: "renderHint",
    outputs: "outputsHint",
  };
  return hints[stage];
}

function statusLabel(locale: Locale, status: StageStatus): string {
  const labels: Record<Locale, Record<StageStatus, string>> = {
    vi: {
      not_started: "Chưa chạy",
      queued: "Đang chờ",
      running: "Đang thực hiện",
      review_required: "Cần kiểm tra",
      approved: "Đã duyệt",
      stale: "Cần chạy lại",
      failed: "Có lỗi",
      cancelled: "Đã hủy",
    },
    en: {
      not_started: "Not started",
      queued: "Queued",
      running: "In progress",
      review_required: "Needs review",
      approved: "Approved",
      stale: "Run again",
      failed: "Failed",
      cancelled: "Cancelled",
    },
  };
  return labels[locale][status];
}

function persistentStage(_stage: StageId): _stage is PersistentStageId {
  return true;
}

function previousStage(stage: StageId): PersistentStageId | null {
  const prerequisites: Record<StageId, PersistentStageId | null> = {
    source: null,
    translation: "source",
    dubbing_audio: "translation",
    render: "dubbing_audio",
    outputs: "render",
  };
  return prerequisites[stage];
}

function automaticNextStage(currentStage: string): StageId | null {
  switch (currentStage) {
    case "source_approved":
      return "translation";
    case "translation_approved":
      return "dubbing_audio";
    case "dubbing_audio_approved":
      return "render";
    case "dubbing_video_approved":
      return "outputs";
    default:
      return null;
  }
}

function latestRun(
  snapshot: ProjectSnapshot | null,
  stage: PersistentStageId,
): StageRun | undefined {
  return Array.isArray(snapshot?.stage_runs)
    ? snapshot.stage_runs.filter((run) => run.stage === stage).at(-1)
    : undefined;
}

function formatRunTime(locale: Locale, value?: string): string {
  if (!value) return "—";
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) return value;
  return new Intl.DateTimeFormat(locale === "vi" ? "vi-VN" : "en-GB", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(timestamp);
}

function formatElapsed(
  locale: Locale,
  startedAt: string | undefined,
  now: number,
): string {
  const started = startedAt ? Date.parse(startedAt) : Number.NaN;
  if (Number.isNaN(started)) return "—";
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = seconds % 60;
  if (locale === "vi")
    return hours
      ? `${hours} giờ ${minutes} phút ${remainder} giây`
      : `${minutes} phút ${remainder} giây`;
  return hours
    ? `${hours}h ${minutes}m ${remainder}s`
    : `${minutes}m ${remainder}s`;
}

function safePercent(value: number | undefined): number {
  return Math.max(0, Math.min(100, Number.isFinite(value) ? Number(value) : 0));
}

function workflowStepTitle(locale: Locale, id: string): string {
  const titles: Record<string, Record<Locale, string>> = {
	translation_prepare: { vi: "Chuẩn bị SRT đã duyệt", en: "Prepare approved SRT" },
	translation_model: { vi: "Dịch các cue bằng model", en: "Translate cues with the model" },
	translation_write: { vi: "Ghi SRT để kiểm tra", en: "Write reviewable SRT" },
    download_video: { vi: "Tải video nguồn", en: "Download source video" },
    download_audio: { vi: "Tải audio nguồn", en: "Download source audio" },
    speech_to_text: {
      vi: "Speech-to-text (tạo transcript)",
      en: "Speech-to-text (create transcript)",
    },
    visual_ocr: {
      vi: "OCR khung hình (trích xuất phụ đề hiển thị)",
      en: "Visual OCR (extract displayed captions)",
    },
    source_srt: {
      vi: "Tạo SRT gốc để kiểm tra",
      en: "Create source SRT for review",
    },
    prepare: { vi: "Chuẩn bị SRT đã duyệt", en: "Prepare approved SRT" },
    synthesize: { vi: "Tạo các đoạn giọng nói", en: "Create speech blocks" },
    fit: { vi: "Khớp thời lượng phụ đề", en: "Fit subtitle timing" },
    assemble: {
      vi: "Ghép thành audio lồng tiếng",
      en: "Assemble dubbed audio",
    },
    render_preflight: {
      vi: "Chuẩn bị media và FFmpeg",
      en: "Prepare media and FFmpeg",
    },
    render_subtitle: {
      vi: "Tạo phụ đề để render",
      en: "Create render-ready subtitles",
    },
    render_encode: {
      vi: "Mã hóa video và chèn phụ đề",
      en: "Encode video and burn subtitles",
    },
    render_verify: {
      vi: "Kiểm tra video đầu ra",
      en: "Verify output video",
    },
  };
  return titles[id]?.[locale] ?? id;
}

function sourceStepStateLabel(
  locale: Locale,
  state: WorkflowProgressStep["state"],
): string {
  const labels: Record<
    WorkflowProgressStep["state"],
    Record<Locale, string>
  > = {
    pending: { vi: "Chờ", en: "Pending" },
    running: { vi: "Đang thực hiện", en: "In progress" },
    completed: { vi: "Hoàn tất", en: "Completed" },
    failed: { vi: "Có lỗi", en: "Failed" },
  };
  return labels[state]?.[locale] ?? state;
}

function workflowArtifactURL(apiBaseURL: string, downloadURL: string): string {
  if (!downloadURL) return "#";
  try {
    return new URL(downloadURL, apiBaseURL).toString();
  } catch {
    return "#";
  }
}

function formatMediaTime(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "00:00";
  const total = Math.floor(value);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  return hours
    ? `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
    : `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function formatFileSize(value: number | undefined): string {
	const bytes = Number(value);
	if (!Number.isFinite(bytes) || bytes < 0) return "—";
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.max(minimum, Math.min(maximum, value));
}

function normalizedNumber(value: string, fallback: number): number {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

type BlurDrag = {
  mode: "move" | "resize";
  startClientX: number;
  startClientY: number;
  x: number;
  y: number;
  width: number;
  height: number;
};

function statusesFor(
  snapshot: ProjectSnapshot | null,
): Record<StageId, StageStatus> {
  if (!snapshot) return initialStatuses;
  return {
    source: latestRun(snapshot, "source")?.status ?? "not_started",
    translation: latestRun(snapshot, "translation")?.status ?? "not_started",
    dubbing_audio:
      latestRun(snapshot, "dubbing_audio")?.status ?? "not_started",
    render: latestRun(snapshot, "render")?.status ?? "not_started",
    outputs: latestRun(snapshot, "outputs")?.status ?? "not_started",
  };
}

export default function App() {
  const [data, setData] = useState<DesktopBootstrap>(emptyBootstrap);
  const [locale, setLocale] = useState<Locale>("vi");
  const [activeStage, setActiveStage] = useState<StageId>("source");
	const [autoTabOpen, setAutoTabOpen] = useState(false);
	const [automationActive, setAutomationActive] = useState(false);
	const [automationURL, setAutomationURL] = useState("");
	const autoAdvanceRef = useRef(new Set<string>());
  const [projects, setProjects] = useState<Project[]>([]);
  const [snapshot, setSnapshot] = useState<ProjectSnapshot | null>(null);
  const [projectName, setProjectName] = useState("");
  const [draft, setDraft] = useState("");
  const [loadedDraftKey, setLoadedDraftKey] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [workerUrl, setWorkerUrl] = useState("");
  const [workerToken, setWorkerToken] = useState("");
  const [connectionMessage, setConnectionMessage] = useState("");
  const [ttsOptions, setTTSOptions] = useState<TTSOption[]>([]);
  const [ttsOptionID, setTTSOptionID] = useState("omnivoice");
  const [sttOptions, setSTTOptions] = useState<STTOption[]>([]);
  const [sttOptionID, setSTTOptionID] = useState("colab-fasterwhisper-medium");
  const [sttWorkerURL, setSTTWorkerURL] = useState("");
  const [sttWorkerToken, setSTTWorkerToken] = useState("");
  const [sttConnectionMessage, setSTTConnectionMessage] = useState("");
	const [sourceMethod, setSourceMethod] = useState<
		"speech_to_text" | "visual_ocr" | "speech_to_text_and_visual_ocr"
	>("speech_to_text");
	// Auto uses an isolated KOVA-owned browser profile for Douyin. The browser
	// runs the platform's current JavaScript signature flow and KOVA captures
	// the resulting media request; no personal Chrome/Edge profile is read.
	const [sourceCookieBrowser, setSourceCookieBrowser] = useState<"auto" | "none" | "chrome" | "edge">("auto");
	const [reviewMode, setReviewMode] = useState<"manual" | "auto">("manual");
	const [originLanguage, setOriginLanguage] = useState("auto");
	const [targetLanguage, setTargetLanguage] = useState("vi");
  const [ocrLanguage, setOCRLanguage] = useState("en");
	const [ocrEngine, setOCREngine] = useState<"colab" | "local">("colab");
	const [ocrWorkerURL, setOCRWorkerURL] = useState("");
	const [ocrWorkerToken, setOCRWorkerToken] = useState("");
	const [ocrConnectionMessage, setOCRConnectionMessage] = useState("");
	const [ocrHealthMessage, setOCRHealthMessage] = useState("");
  const [ocrRegionX, setOCRRegionX] = useState("0.10");
  const [ocrRegionY, setOCRRegionY] = useState("0.70");
  const [ocrRegionWidth, setOCRRegionWidth] = useState("0.80");
  const [ocrRegionHeight, setOCRRegionHeight] = useState("0.20");
  const [ocrIntervalMS, setOCRIntervalMS] = useState("250");
  const [ocrPreferGPU, setOCRPreferGPU] = useState(true);
	// Hybrid OCR is an enhancement to the timed STT source. In Auto mode this
	// remains enabled by default, but it must never make an otherwise-ready
	// end-to-end job fail merely because optional PaddleOCR is not installed.
	const [ocrFallbackToSTT, setOCRFallbackToSTT] = useState(true);
	const [blurOriginalText, setBlurOriginalText] = useState(true);
	const [blurRegionX, setBlurRegionX] = useState("0.10");
	const [blurRegionY, setBlurRegionY] = useState("0.70");
	const [blurRegionWidth, setBlurRegionWidth] = useState("0.80");
	const [blurRegionHeight, setBlurRegionHeight] = useState("0.20");
	const [blurStrength, setBlurStrength] = useState("8");
	const [blurDrag, setBlurDrag] = useState<BlurDrag | null>(null);
	const blurPreviewRef = useRef<HTMLDivElement | null>(null);
	const [capCutSettings, setCapCutSettings] = useState<CapCutDraftSettings>({
		enabled: false,
		backend: "pycapcut",
		draft_root: "",
		python_path: "python",
	});
  const [translationModels, setTranslationModels] = useState<
    TranslationModelOption[]
  >([]);
  const [translationModelID, setTranslationModelID] = useState(
    "oc/deepseek-v4-flash-free",
  );
	const [gatewayAPIKey, setGatewayAPIKey] = useState("");
  const [voiceProfiles, setVoiceProfiles] = useState<VoiceProfile[]>([]);
  const [voiceProfileID, setVoiceProfileID] = useState("");
	const [voiceProfileName, setVoiceProfileName] = useState("");
	const [voiceReferencePath, setVoiceReferencePath] = useState("");
	const [voiceCloneConsent, setVoiceCloneConsent] = useState(false);
	const [voicePreviewURL, setVoicePreviewURL] = useState("");
	const [voicePreviewMessage, setVoicePreviewMessage] = useState("");
  const [workflowStatus, setWorkflowStatus] =
    useState<DesktopWorkflowSnapshot | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [previewTime, setPreviewTime] = useState(0);
  const [previewDuration, setPreviewDuration] = useState(0);
  const [previewError, setPreviewError] = useState("");
	const [previewReloadToken, setPreviewReloadToken] = useState(0);

  const stage = useMemo(
    () => data.stages.find((item) => item.id === activeStage),
    [activeStage, data.stages],
  );
  const statuses = useMemo(() => statusesFor(snapshot), [snapshot]);
  const activeRun = persistentStage(activeStage)
    ? latestRun(snapshot, activeStage)
    : undefined;
  const selectedTTS = ttsOptions.find((option) => option.id === ttsOptionID);
  const selectedSTT = sttOptions.find((option) => option.id === sttOptionID);
  const prerequisite = previousStage(activeStage);
  const sourceRequiresSTTWorker =
	(sourceMethod === "speech_to_text" || sourceMethod === "speech_to_text_and_visual_ocr") && selectedSTT?.needs_worker;
	const sourceUsesOCR = sourceMethod === "visual_ocr" || sourceMethod === "speech_to_text_and_visual_ocr";
	const sourceRequiresOCRWorker = sourceUsesOCR && ocrEngine === "colab";
  const canStart = Boolean(
    snapshot &&
    activeRun?.status !== "running" &&
    (!prerequisite || statuses[prerequisite] === "approved") &&
	(activeStage !== "source" || draft.trim()) &&
	(activeStage !== "translation" ||
		(!sourceRequiresSTTWorker ||
		  (sttWorkerURL.trim() && sttWorkerToken.trim())) &&
		(!sourceRequiresOCRWorker ||
		  (ocrWorkerURL.trim() && ocrWorkerToken.trim()))),
  );
  const stageNeedsTextReview =
	activeStage === "translation";
  const canSaveDraft = Boolean(
    stageNeedsTextReview &&
    snapshot &&
    activeRun &&
    activeRun.status === "review_required" &&
    draft.trim(),
  );
  const canApprove = Boolean(
    activeRun &&
    activeRun.status === "review_required" &&
    (!stageNeedsTextReview || draft.trim()),
  );

  useEffect(() => {
    void (async () => {
      try {
        const [
          nextBootstrap,
          nextOptions,
          nextSTTOptions,
          nextTranslationModels,
          nextProjects,
          nextVoiceProfiles,
		  nextCapCutSettings,
        ] = await Promise.all([
          bootstrap(),
          listTTSOptions(),
          listSTTOptions(),
          listTranslationModels(),
          listDesktopProjects(),
          listVoiceProfiles("", ""),
		  getCapCutDraftSettings(),
        ]);
        setData(nextBootstrap);
        setTTSOptions(nextOptions);
        setSTTOptions(nextSTTOptions);
        if (
          nextSTTOptions.some(
            (option) => option.id === "colab-fasterwhisper-medium",
          )
        )
          setSTTOptionID("colab-fasterwhisper-medium");
        setTranslationModels(nextTranslationModels);
		setVoiceProfiles(nextVoiceProfiles);
		setCapCutSettings(nextCapCutSettings);
		if (nextVoiceProfiles[0]) {
			setVoiceProfileID(nextVoiceProfiles[0].id);
			if (nextVoiceProfiles[0].worker_url) setWorkerUrl(nextVoiceProfiles[0].worker_url);
		}
        if (nextTranslationModels[0])
          setTranslationModelID(nextTranslationModels[0].id);
        setProjects(nextProjects);
        if (nextProjects[0])
          setSnapshot(await getDesktopProject(nextProjects[0].id));
      } catch (error) {
        setMessage(`${t("vi", "actionFailed")} ${asMessage(error)}`);
        setData(await bootstrap());
      }
    })();
  }, []);

  useEffect(() => {
    const projectID = snapshot?.project.id;
    const workflowTaskID = snapshot?.project.workflow_task_id;
    if (
      !projectID ||
      !workflowTaskID ||
      workflowStatus?.workflow_task_id === workflowTaskID
    )
      return;
    let cancelled = false;
    void refreshDesktopWorkflow(projectID)
      .then((nextWorkflow) => {
        if (!cancelled) setWorkflowStatus(nextWorkflow);
      })
      .catch((error) => {
        if (!cancelled)
          setMessage(
            `${locale === "vi" ? "Không thể tải trạng thái/artifact của worker:" : "Could not load worker status/artifacts:"} ${asMessage(error)}`,
          );
      });
    return () => {
      cancelled = true;
    };
  }, [
    locale,
    snapshot?.project.id,
    snapshot?.project.workflow_task_id,
    workflowStatus?.workflow_task_id,
  ]);

  useEffect(() => {
    const projectID = snapshot?.project.id;
    const subtitleURL = activeStage === "translation"
      ? workflowStatus?.translated_srt_url
      : "";
    if (
      !projectID ||
      !subtitleURL ||
	  activeStage !== "translation"
    )
      return;
    const key = `${projectID}:${activeStage}:${subtitleURL}`;
    if (loadedDraftKey === key) return;
    let cancelled = false;
    void readDesktopWorkflowSubtitle(projectID, activeStage)
      .then((content) => {
        if (cancelled) return;
        setDraft(content);
        setLoadedDraftKey(key);
      })
      .catch((error) => {
        if (!cancelled)
          setMessage(
            `${locale === "vi" ? "Không thể mở SRT để kiểm tra:" : "Could not open the review SRT:"} ${asMessage(error)}`,
          );
      });
    return () => {
      cancelled = true;
    };
  }, [
    activeStage,
    loadedDraftKey,
    locale,
    snapshot?.project.id,
    workflowStatus?.source_srt_url,
    workflowStatus?.translated_srt_url,
  ]);

  useEffect(() => {
    if (activeRun?.status !== "running" && !automationActive) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [activeRun?.id, activeRun?.status, automationActive]);

  useEffect(() => {
    const projectID = snapshot?.project.id;
    const workflowTaskID = snapshot?.project.workflow_task_id;
    if (
      !projectID ||
      !workflowTaskID ||
      (activeRun?.status !== "running" && !automationActive)
    )
      return;
    let cancelled = false;
    let inFlight = false;
    const poll = async () => {
	  if (inFlight) return;
	  inFlight = true;
      try {
        const nextWorkflow = await refreshDesktopWorkflow(projectID);
        if (cancelled) return;
        setWorkflowStatus(nextWorkflow);
        setMessage(
          nextWorkflow.failure_reason
            ? `${locale === "vi" ? "Lỗi worker:" : "Worker error:"} ${nextWorkflow.failure_reason}`
            : "",
        );
        setSnapshot(await getDesktopProject(projectID));
      } catch (error) {
        if (!cancelled)
          setMessage(
            `${locale === "vi" ? "Không thể cập nhật trạng thái worker:" : "Could not refresh worker status:"} ${asMessage(error)}`,
          );
	  } finally {
		inFlight = false;
      }
    };
    void poll();
    // FFmpeg emits its encoded timestamp continuously. Poll once per second
    // so the desktop reflects each independent sub-task without overlapping
    // network calls when a local request temporarily takes longer.
    const timer = window.setInterval(() => void poll(), 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [
    activeRun?.id,
    activeRun?.status,
    automationActive,
    locale,
    snapshot?.project.id,
    snapshot?.project.workflow_task_id,
  ]);

  // Auto mode deliberately starts one persisted stage at a time.  The worker
  // still owns every heavy operation, while the desktop waits for the
  // auto-approved hand-off before it submits the next configured stage.
  useEffect(() => {
    const projectID = snapshot?.project.id;
    if (!automationActive || !projectID || !workflowStatus) return;

    if (workflowStatus.failure_reason || workflowStatus.current_stage === "failed") {
      setAutomationActive(false);
      return;
    }
    if (workflowStatus.current_stage === "completed") {
      setAutomationActive(false);
      setMessage(
        locale === "vi"
          ? "Luồng tự động đã hoàn tất. Video cuối và các artifact đã sẵn sàng ở bước 05."
          : "Automatic workflow completed. The final video and artifacts are ready in step 05.",
      );
      return;
    }

    const nextStage = automaticNextStage(workflowStatus.current_stage);
    if (!nextStage) return;
    const prerequisite = previousStage(nextStage);
    if (!prerequisite || latestRun(snapshot, prerequisite)?.status !== "approved")
      return;
    const nextRun = latestRun(snapshot, nextStage);
    if (
      nextRun?.status === "running" ||
      nextRun?.status === "review_required" ||
      nextRun?.status === "approved"
    )
      return;

    const transitionKey = `${projectID}:${workflowStatus.current_stage}:${nextStage}`;
    if (autoAdvanceRef.current.has(transitionKey)) return;
    autoAdvanceRef.current.add(transitionKey);
    let cancelled = false;
    void (async () => {
      try {
        await startDesktopWorkflowStage(
          buildWorkflowRequest(projectID, nextStage, "", "auto"),
        );
        if (!cancelled) {
          await refreshProjects(projectID);
          setMessage("");
        }
      } catch (error) {
        if (!cancelled) {
          setAutomationActive(false);
          setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [
    automationActive,
    blurOriginalText,
    blurRegionHeight,
    blurRegionWidth,
    blurRegionX,
    blurRegionY,
    blurStrength,
		gatewayAPIKey,
    locale,
		ocrEngine,
    ocrIntervalMS,
    ocrLanguage,
		ocrWorkerToken,
		ocrWorkerURL,
    ocrPreferGPU,
    ocrRegionHeight,
    ocrRegionWidth,
    ocrRegionX,
    ocrRegionY,
    snapshot,
    sourceMethod,
    sttOptionID,
    sttWorkerToken,
    sttWorkerURL,
    translationModelID,
    ttsOptionID,
    voiceProfileID,
    workerToken,
    workerUrl,
    workflowStatus,
  ]);

  async function refreshProjects(selectID?: string) {
    const nextProjects = await listDesktopProjects();
    setProjects(nextProjects);
    const id = selectID ?? snapshot?.project.id ?? nextProjects[0]?.id;
    if (id) setSnapshot(await getDesktopProject(id));
  }

  async function handleCreateProject() {
    if (!projectName.trim()) return;
    await withBusy(async () => {
      const created = await createDesktopProject(projectName.trim(), targetLanguage);
      setProjectName("");
      setWorkflowStatus(null);
      setLoadedDraftKey("");
      await refreshProjects(created.id);
      setMessage("");
    });
  }

  async function handleDeleteProject() {
    if (!snapshot) return;
    const name = snapshot.project.name;
    const prompt =
      locale === "vi"
        ? `Xóa dự án “${name}”, toàn bộ draft và artifact của job này? Thao tác không thể hoàn tác.`
        : `Delete “${name}” with all of its drafts and job artifacts? This cannot be undone.`;
    if (typeof window !== "undefined" && !window.confirm(prompt)) return;
    await withBusy(async () => {
      const removedID = snapshot.project.id;
      await deleteDesktopProject(removedID);
      const remaining = await listDesktopProjects();
      setProjects(remaining);
      setWorkflowStatus(null);
      setLoadedDraftKey("");
      setDraft("");
      setMessage("");
      if (remaining[0]) {
        setSnapshot(await getDesktopProject(remaining[0].id));
      } else {
        setSnapshot(null);
      }
    });
  }

  async function handleSelectProject(projectID: string) {
    await withBusy(async () => {
      const selected = await getDesktopProject(projectID);
      setSnapshot(selected);
      setTargetLanguage(selected.project.target_language || "vi");
      setWorkflowStatus(null);
      setLoadedDraftKey("");
      setMessage("");
    });
  }

  async function handleSelectSourceVideo(destination: "draft" | "automation") {
    try {
      const source = await selectSourceVideo();
      if (!source) return;
      if (destination === "automation") {
        setAutomationURL(source);
      } else {
        setDraft(source);
      }
      setMessage("");
    } catch (error) {
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    }
  }

  async function handleOpenShortVideoSession(sourceURL: string) {
    const url = sourceURL.trim();
    if (!url || (!url.toLowerCase().includes("douyin.com") && !url.toLowerCase().includes("tiktok.com"))) {
      setMessage(
        locale === "vi"
          ? "Hãy nhập URL Douyin hoặc TikTok trước khi thiết lập phiên."
          : "Enter a Douyin or TikTok URL before setting up the session.",
      );
      return;
    }
    try {
      await openShortVideoSession(url, sourceCookieBrowser);
      setMessage(
        locale === "vi"
          ? "Đã mở profile trình duyệt riêng của KOVA. Nếu nền tảng yêu cầu, hãy đăng nhập hoặc xác minh, phát video một lần, đóng cửa sổ trình duyệt rồi bấm Bắt đầu lại."
          : "KOVA's isolated browser profile is open. If requested, sign in or complete verification, play the video once, close the browser, then start the step again.",
      );
    } catch (error) {
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    }
  }

  async function handleChooseCapCutDraftRoot() {
    try {
      const root = await selectCapCutDraftRoot();
      if (!root) return;
      setCapCutSettings((current) => ({ ...current, draft_root: root }));
      setMessage("");
    } catch (error) {
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    }
  }

  async function handleSaveCapCutDraftSettings() {
    await withBusy(async () => {
      await saveCapCutDraftSettings(capCutSettings);
      const refreshed = await getCapCutDraftSettings();
      setCapCutSettings(refreshed);
      setMessage(
        locale === "vi"
          ? "Đã lưu cấu hình xuất project CapCut chỉnh sửa được. Lần xuất kế tiếp sẽ tạo track video, audio và phụ đề riêng."
          : "Editable CapCut project export is saved. The next export will create separate video, audio, and subtitle tracks.",
      );
    });
  }

  async function handleVisualOCRSetup(install: boolean) {
    setBusy(true);
    try {
      const health = install ? await installVisualOCR() : await checkVisualOCR();
      setOCRHealthMessage(health.message || "");
      if (!health.ready) {
        setMessage(health.message || (locale === "vi" ? "OCR chưa sẵn sàng." : "OCR is not ready."));
      } else {
        setMessage("");
      }
    } catch (error) {
      setOCRHealthMessage(asMessage(error));
    } finally {
      setBusy(false);
    }
  }

  function startBlurDrag(
    event: ReactPointerEvent<HTMLElement>,
    mode: BlurDrag["mode"],
  ) {
    if (!blurOriginalText || !blurPreviewRef.current) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture?.(event.pointerId);
    setBlurDrag({
      mode,
      startClientX: event.clientX,
      startClientY: event.clientY,
      ...previewBlurRect,
    });
  }

  function updateBlurDrag(event: ReactPointerEvent<HTMLElement>) {
    if (!blurDrag || !blurPreviewRef.current) return;
    const bounds = blurPreviewRef.current.getBoundingClientRect();
    if (bounds.width <= 0 || bounds.height <= 0) return;
    const deltaX = (event.clientX - blurDrag.startClientX) / bounds.width;
    const deltaY = (event.clientY - blurDrag.startClientY) / bounds.height;

    if (blurDrag.mode === "move") {
      const x = clamp(blurDrag.x + deltaX, 0, 1 - blurDrag.width);
      const y = clamp(blurDrag.y + deltaY, 0, 1 - blurDrag.height);
      setBlurRegionX(x.toFixed(3));
      setBlurRegionY(y.toFixed(3));
      return;
    }

    const width = clamp(blurDrag.width + deltaX, 0.02, 1 - blurDrag.x);
    const height = clamp(blurDrag.height + deltaY, 0.02, 1 - blurDrag.y);
    setBlurRegionWidth(width.toFixed(3));
    setBlurRegionHeight(height.toFixed(3));
  }

  function finishBlurDrag(event?: ReactPointerEvent<HTMLElement>) {
    if (event?.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    setBlurDrag(null);
  }

  function buildWorkflowRequest(
    projectID: string,
    stage: StageId,
    sourceURL: string,
    mode: "manual" | "auto" = reviewMode,
  ) {
    return {
      project_id: projectID,
      stage,
      source_url: sourceURL,
		origin_language: originLanguage,
		target_language: targetLanguage,
      stt_option_id: sttOptionID,
      stt_worker_url: sttWorkerURL,
      stt_worker_token: sttWorkerToken,
      source_method: sourceMethod,
      review_mode: mode,
		source_cookie_browser: sourceCookieBrowser,
		ocr_engine: ocrEngine,
		ocr_worker_url: ocrWorkerURL,
		ocr_worker_token: ocrWorkerToken,
      ocr_language: ocrLanguage,
      ocr_region_x: Number(ocrRegionX),
      ocr_region_y: Number(ocrRegionY),
      ocr_region_width: Number(ocrRegionWidth),
      ocr_region_height: Number(ocrRegionHeight),
      ocr_sample_interval_ms: Number(ocrIntervalMS),
      ocr_prefer_gpu: ocrPreferGPU,
		ocr_fallback_to_stt: ocrFallbackToSTT,
      blur_original_text: blurOriginalText,
      blur_region_x: Number(blurRegionX),
      blur_region_y: Number(blurRegionY),
      blur_region_width: Number(blurRegionWidth),
      blur_region_height: Number(blurRegionHeight),
      blur_strength: Number(blurStrength),
      translation_model_id: translationModelID,
		gateway_api_key: gatewayAPIKey,
      tts_option_id: ttsOptionID,
      voice_profile_id: voiceProfileID,
      worker_url: workerUrl,
      worker_token: workerToken,
    };
  }

  async function handleStartStage() {
    if (!snapshot) return;
    const projectID = snapshot.project.id;
    setBusy(true);
    try {
      const action = await startDesktopWorkflowStage(
        buildWorkflowRequest(
          projectID,
          activeStage,
          activeStage === "source" ? draft.trim() : "",
        ),
      );
      await refreshProjects(projectID);
      setWorkflowStatus({
        workflow_task_id: action.workflow_task_id ?? "",
        current_stage: "starting",
        process_percent: 0,
        message:
          locale === "vi"
            ? "Đã gửi yêu cầu. KOVA đang chờ worker nhận job."
            : "Request sent. KOVA is waiting for the worker to accept the job.",
        review_required: false,
      });
      setMessage("");
    } catch (error) {
      try {
        await refreshProjects(projectID);
      } catch {
        /* Preserve the actionable start error below. */
      }
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    } finally {
      setBusy(false);
    }
  }

  async function handleStartAutomation() {
    const sourceURL = automationURL.trim();
    if (!sourceURL) {
      setMessage(locale === "vi" ? "Hãy dán URL video trước khi chạy tự động." : "Paste a video URL before starting automation.");
      return;
    }
    setBusy(true);
    try {
      if (sourceRequiresSTTWorker) {
        const health = await checkSTTHealth(sttWorkerURL, sttWorkerToken);
        if (!health.reachable) throw new Error(health.message || "STT Colab worker is unavailable.");
      }
		if (sourceRequiresOCRWorker) {
			const health = await checkOCRHealth(ocrWorkerURL, ocrWorkerToken);
			if (!health.reachable) throw new Error(health.message || "OCR Colab worker is unavailable.");
		}
      if (selectedTTS?.needs_worker) {
        if (!voiceProfileID.trim()) throw new Error(locale === "vi" ? "Chọn một profile giọng cố định trước khi chạy tự động." : "Select a fixed voice profile before automation.");
        const health = await checkVoiceHealth(workerUrl, workerToken);
        if (!health.reachable) throw new Error(health.message || "Voice Studio worker is unavailable.");
      }

      let projectID = snapshot?.project.id ?? "";
      let currentProject = projectID
        ? await getDesktopProject(projectID)
        : null;
      // A project represents one immutable source workflow.  In the one-URL
      // experience, make a fresh project instead of rejecting the user just
      // because the last project shown in the selector has already run.
      if (!currentProject || currentProject.project.workflow_task_id) {
        const created = await createDesktopProject(
          projectName.trim() || `KOVA Auto ${new Date().toLocaleString("sv-SE").replace(/[ :]/g, "-")}`,
          targetLanguage,
        );
        projectID = created.id;
        await refreshProjects(projectID);
        currentProject = await getDesktopProject(projectID);
      }

      const action = await startDesktopWorkflowStage(
        buildWorkflowRequest(projectID, "source", sourceURL, "auto"),
      );
      autoAdvanceRef.current.clear();
      setAutomationActive(true);
      setReviewMode("auto");
      setWorkflowStatus({
        workflow_task_id: action.workflow_task_id ?? "",
        current_stage: "starting",
        process_percent: 0,
        message: locale === "vi" ? "Đã khởi động luồng tự động." : "Automatic workflow started.",
        review_required: false,
        review_mode: "auto",
      });
      await refreshProjects(projectID);
      setMessage("");
    } catch (error) {
      setAutomationActive(false);
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    } finally {
      setBusy(false);
    }
  }

  async function handleSaveDraft() {
    if (!snapshot || !activeRun || !draft.trim()) return;
    await withBusy(async () => {
      await saveDesktopWorkflowDraft(
        snapshot.project.id,
        activeRun.id,
        activeStage,
        draft,
      );
      if (snapshot.project.workflow_task_id)
        setWorkflowStatus(await refreshDesktopWorkflow(snapshot.project.id));
      await refreshProjects(snapshot.project.id);
    });
  }

  async function handleMarkForReview() {
    if (!snapshot || !activeRun) return;
    await withBusy(async () => {
      await markDesktopStageForReview(activeRun.id, "stage.review_required");
      await refreshProjects(snapshot.project.id);
    });
  }

  async function handleApprove() {
    if (!snapshot || !activeRun) return;
    await withBusy(async () => {
      await approveDesktopWorkflowStage(
        snapshot.project.id,
        activeRun.id,
        activeStage,
      );
      await refreshProjects(snapshot.project.id);
    });
  }

  async function handleRefreshWorkflow() {
    if (!snapshot) return;
    await withBusy(async () => {
      const workflow = await refreshDesktopWorkflow(snapshot.project.id);
      setWorkflowStatus(workflow);
      setMessage(
        workflow.failure_reason
          ? `${locale === "vi" ? "Lỗi worker:" : "Worker error:"} ${workflow.failure_reason}`
          : "",
      );
      await refreshProjects(snapshot.project.id);
    });
  }

  async function handleConnectionCheck() {
    const result = await checkVoiceHealth(workerUrl, workerToken);
    setConnectionMessage(
      result.reachable
        ? t(locale, "connectionSuccess")
        : `${t(locale, "connectionFailed")} ${result.message}`,
    );
  }

  async function handleSTTConnectionCheck() {
    const result = await checkSTTHealth(sttWorkerURL, sttWorkerToken);
    setSTTConnectionMessage(
      result.reachable
        ? locale === "vi"
          ? "Worker STT Colab CUDA đã sẵn sàng."
          : "Colab CUDA STT worker is ready."
        : `${locale === "vi" ? "Không thể kết nối worker STT Colab." : "Cannot connect to the Colab STT worker."} ${result.message}`,
    );
  }

  async function handleOCRConnectionCheck() {
	const result = await checkOCRHealth(ocrWorkerURL, ocrWorkerToken);
	setOCRConnectionMessage(
	  result.reachable
		? locale === "vi"
		  ? "Worker OCR Colab CUDA đã sẵn sàng."
		  : "Colab CUDA OCR worker is ready."
		: `${locale === "vi" ? "Không thể kết nối worker OCR Colab." : "Cannot connect to the Colab OCR worker."} ${result.message}`,
	);
  }

  async function handleLoadProfiles() {
    await withBusy(async () => {
      const profiles = await listVoiceProfiles(workerUrl, workerToken);
      setVoiceProfiles(profiles);
		if (!voiceProfileID && profiles[0]) {
			setVoiceProfileID(profiles[0].id);
			if (profiles[0].worker_url) setWorkerUrl(profiles[0].worker_url);
		}
      setConnectionMessage(t(locale, "profilesLoaded"));
    });
  }

  function handleVoiceProfileSelection(profileID: string) {
    setVoiceProfileID(profileID);
    setVoicePreviewURL("");
    setVoicePreviewMessage("");
    const selected = voiceProfiles.find((profile) => profile.id === profileID);
    // A profile saved from an earlier Colab runtime remembers only its public
    // worker URL. Its token is intentionally never persisted. Reusing the URL
    // saves the user from retyping it while still requiring the current token.
    if (selected?.worker_url) setWorkerUrl(selected.worker_url);
  }

  async function handleChooseVoiceReference() {
    try {
      const selectedPath = await selectVoiceReferenceAudio();
      if (selectedPath) {
        setVoiceReferencePath(selectedPath);
        setConnectionMessage("");
      }
    } catch (error) {
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    }
  }

  async function handleCreateVoiceProfile() {
    await withBusy(async () => {
      const created = await createVoiceProfile({
        base_url: workerUrl,
        token: workerToken,
        name: voiceProfileName,
        reference_audio_path: voiceReferencePath,
        language: "vi",
        consent_confirmed: voiceCloneConsent,
      });
      const profiles = await listVoiceProfiles(workerUrl, workerToken);
      setVoiceProfiles(profiles);
      setVoiceProfileID(created.id);
      setVoiceProfileName("");
      setVoiceReferencePath("");
      setVoiceCloneConsent(false);
	  setVoicePreviewURL("");
	  setVoicePreviewMessage("");
      setConnectionMessage(
        locale === "vi"
          ? `Đã tạo profile giọng “${created.name || created.id}”.`
          : `Voice profile “${created.name || created.id}” was created.`,
      );
    });
  }

  async function handlePreviewVoiceProfile() {
    const selected = voiceProfiles.find((profile) => profile.id === voiceProfileID);
    await withBusy(async () => {
      const preview = await previewVoiceProfile({
        base_url: workerUrl,
        token: workerToken,
        profile_id: voiceProfileID,
        language: selected?.language || "vi",
      });
      setVoicePreviewURL(preview.data_url);
      setVoicePreviewMessage(
        locale === "vi"
          ? "Đây là câu nghe thử. Bạn có thể phát lại trước khi dùng giọng này."
          : "This is a short preview. You can replay it before using this voice.",
      );
    });
  }

  async function handleDeleteVoiceProfile() {
    const selected = voiceProfiles.find((profile) => profile.id === voiceProfileID);
    if (!selected) return;
    const prompt = locale === "vi"
      ? `Xóa giọng “${selected.name || selected.id}” khỏi thư viện KOVA? File mẫu đã lưu cục bộ sẽ bị xóa.`
      : `Delete “${selected.name || selected.id}” from the KOVA voice library? Its locally saved reference will be removed.`;
    if (!window.confirm(prompt)) return;
    await withBusy(async () => {
      await deleteVoiceProfile({
        base_url: workerUrl,
        token: workerToken,
        profile_id: selected.id,
      });
      const profiles = await listVoiceProfiles(workerUrl, workerToken);
      setVoiceProfiles(profiles);
      const nextProfile = profiles[0];
      setVoiceProfileID(nextProfile?.id || "");
      if (nextProfile?.worker_url) setWorkerUrl(nextProfile.worker_url);
      setVoicePreviewURL("");
      setVoicePreviewMessage(
        locale === "vi" ? "Đã xóa giọng khỏi thư viện KOVA." : "The voice was removed from the KOVA library.",
      );
    });
  }

  async function withBusy(action: () => Promise<void>) {
    setBusy(true);
    try {
      await action();
    } catch (error) {
      setMessage(`${t(locale, "actionFailed")} ${asMessage(error)}`);
    } finally {
      setBusy(false);
    }
  }

  const failureDetail =
    workflowStatus?.failure_reason || activeRun?.failure_code || "";
	const workerProcessPercent = safePercent(workflowStatus?.process_percent);
  const sourceSteps =
    activeStage === "source" && Array.isArray(workflowStatus?.source_steps)
      ? workflowStatus.source_steps
		: activeStage === "translation" && Array.isArray(workflowStatus?.translation_steps)
			? workflowStatus.translation_steps
      : activeStage === "dubbing_audio" &&
          Array.isArray(workflowStatus?.dubbing_steps)
        ? workflowStatus.dubbing_steps
        : activeStage === "render" &&
            Array.isArray(workflowStatus?.render_steps)
          ? workflowStatus.render_steps
        : [];
	// Translation has its own progress contract: the legacy worker's 40-70%
	// pipeline milestone must not be presented as a partially completed
	// translation. Use its independent task steps instead.
	const workflowPercent =
		activeStage === "translation" && sourceSteps.length > 0
			? Math.round(
					sourceSteps.reduce(
						(sum, step) =>
							sum +
							(step.state === "completed" || step.state === "failed"
								? 100
								: safePercent(step.percent)),
						0,
					) / sourceSteps.length,
				)
			: workerProcessPercent;
  const workflowStepsHeading =
    activeStage === "render"
      ? locale === "vi"
        ? "Các tác vụ render video"
        : "Video rendering tasks"
		: activeStage === "translation"
			? locale === "vi"
				? "Các tác vụ dịch và tạo phụ đề"
				: "Translation and subtitle tasks"
      : activeStage === "dubbing_audio"
        ? locale === "vi"
          ? "Các tác vụ tạo audio lồng tiếng"
          : "Dubbed-audio processing tasks"
        : locale === "vi"
          ? "Các tác vụ xử lý nguồn"
          : "Source processing tasks";
	// Stage 01 no longer generates an SRT. Keep the source URL editor visible
	// for retrying a download; editable subtitle text belongs to Stage 02.
	const sourceSRTAvailable = false;
  const translationWarnings =
    activeStage === "translation" &&
    Array.isArray(workflowStatus?.translation_warnings)
      ? workflowStatus.translation_warnings
      : [];
  const workflowArtifacts = Array.isArray(workflowStatus?.artifacts)
    ? workflowStatus.artifacts
    : [];
  const dubbedAudioArtifact = workflowArtifacts.find(
    (artifact) => artifact.kind === "dubbed_audio",
  );
  const dubbedAudioURL = dubbedAudioArtifact
    ? workflowArtifactURL(data.legacy_api_base_url, dubbedAudioArtifact.download_url)
    : "";
  const previewArtifact =
    workflowArtifacts.find(
      (artifact) => artifact.kind === "subtitled_horizontal_video",
    ) ??
    workflowArtifacts.find((artifact) => artifact.kind === "dubbed_video") ??
    workflowArtifacts.find((artifact) => artifact.kind === "source_video");
  const previewBaseURL = previewArtifact
    ? workflowArtifactURL(
        data.legacy_api_base_url,
        previewArtifact.download_url,
      )
    : "";
	const previewURL = previewBaseURL
		? `${previewBaseURL}${previewBaseURL.includes("?") ? "&" : "?"}kova_preview=${encodeURIComponent(`${workflowStatus?.updated_at ?? ""}-${previewReloadToken}`)}`
		: "";
	const finalVideo = workflowStatus?.final_video;
	const finalVideoURL = finalVideo
		? workflowArtifactURL(data.legacy_api_base_url, finalVideo.download_url)
		: "";
  const previewPercent =
    previewDuration > 0
      ? safePercent((previewTime / previewDuration) * 100)
      : 0;
  const previewBlurRect = useMemo(() => {
    const x = clamp(normalizedNumber(blurRegionX, 0.1), 0, 0.98);
    const y = clamp(normalizedNumber(blurRegionY, 0.7), 0, 0.98);
    const width = clamp(normalizedNumber(blurRegionWidth, 0.8), 0.02, 1 - x);
    const height = clamp(normalizedNumber(blurRegionHeight, 0.2), 0.02, 1 - y);
    return { x, y, width, height };
  }, [blurRegionHeight, blurRegionWidth, blurRegionX, blurRegionY]);
  useEffect(() => {
    setPreviewTime(0);
    setPreviewDuration(0);
    setPreviewError("");
  }, [previewURL]);
  const stageMessage =
    activeRun?.status === "running"
      ? workflowStatus?.message || t(locale, "stageRunning")
      : activeRun?.status === "review_required"
        ? t(locale, "stageReview")
        : activeRun?.status === "approved"
          ? t(locale, "stageApproved")
          : activeRun?.status === "failed"
            ? locale === "vi"
              ? "Bước thất bại. Xem lỗi chi tiết bên dưới trước khi chạy lại."
              : "The stage failed. Review the detailed error below before retrying."
            : "";

  const voiceProfileCreator = selectedTTS?.needs_profile ? (
    <section className="voice-profile-creator">
      <div>
        <h3>{locale === "vi" ? "Tạo profile clone giọng mới" : "Create a new voice-clone profile"}</h3>
        <p>
          {locale === "vi"
            ? "Tải audio mẫu WAV/MP3/FLAC dài 3–30 giây (khuyến nghị 10 giây). Sau khi tạo thành công, KOVA lưu một bản sao riêng trong Thư viện giọng (không nằm trong dự án và không lưu token) để lần sau mở app vẫn có thể chọn hoặc khôi phục profile trên Colab mới."
            : "Upload a 3–30 second WAV/MP3/FLAC reference clip (10 seconds recommended). After a successful creation, KOVA keeps a private library copy outside projects and never stores the Colab token, so the voice remains selectable after restarting the app."}
        </p>
      </div>
      <label>
        {locale === "vi" ? "Tên giọng" : "Voice name"}
        <input
          value={voiceProfileName}
          disabled={busy || automationActive}
          placeholder={locale === "vi" ? "Ví dụ: Giọng đọc chính" : "Example: Main narrator"}
          onChange={(event) => setVoiceProfileName(event.target.value)}
        />
      </label>
      <label>
        {locale === "vi" ? "Audio mẫu clone giọng" : "Voice-clone reference audio"}
        <div className="voice-file-picker">
          <input
            value={voiceReferencePath}
            readOnly
            placeholder={locale === "vi" ? "Chưa chọn file WAV, MP3 hoặc FLAC" : "No WAV, MP3, or FLAC file selected"}
          />
          <button
            className="secondary"
            type="button"
            disabled={busy || automationActive}
            onClick={() => void handleChooseVoiceReference()}
          >
            {locale === "vi" ? "Chọn audio mẫu…" : "Choose reference…"}
          </button>
        </div>
      </label>
      <label className="voice-consent-toggle">
        <input
          type="checkbox"
          checked={voiceCloneConsent}
          disabled={busy || automationActive}
          onChange={(event) => setVoiceCloneConsent(event.target.checked)}
        />
        {locale === "vi"
          ? "Tôi có quyền và sự đồng ý để dùng audio này tạo profile clone giọng."
          : "I have permission and consent to use this audio to create a cloned-voice profile."}
      </label>
      <button
        className="primary"
        type="button"
        disabled={
          busy ||
          automationActive ||
          !workerUrl.trim() ||
          !workerToken.trim() ||
          !voiceProfileName.trim() ||
          !voiceReferencePath.trim() ||
          !voiceCloneConsent
        }
        onClick={() => void handleCreateVoiceProfile()}
      >
        {locale === "vi" ? "Tạo profile giọng" : "Create voice profile"}
      </button>
      <div className="voice-library-actions">
        <button
          className="secondary"
          type="button"
          disabled={busy || automationActive || !voiceProfileID || !workerUrl.trim() || !workerToken.trim()}
          onClick={() => void handlePreviewVoiceProfile()}
        >
          {locale === "vi" ? "Nghe thử giọng đã chọn" : "Preview selected voice"}
        </button>
        <button
          className="danger"
          type="button"
          disabled={busy || automationActive || !voiceProfileID}
          onClick={() => void handleDeleteVoiceProfile()}
        >
          {locale === "vi" ? "Xóa giọng đã chọn" : "Delete selected voice"}
        </button>
      </div>
      {voicePreviewMessage && <p className="voice-preview-message">{voicePreviewMessage}</p>}
      {voicePreviewURL && (
        <audio className="voice-preview-audio" controls src={voicePreviewURL}>
          {locale === "vi" ? "Trình duyệt không hỗ trợ phát audio." : "Your browser does not support audio playback."}
        </audio>
      )}
    </section>
  ) : null;

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark">K</span>
          <span>KOVA</span>
        </div>
        <div className="project-name">
          {snapshot?.project.name ?? t(locale, "newProject")}
        </div>
        <div className="topbar-actions">
          <span className="status-online">● {t(locale, "ready")}</span>
          <label className="locale-select">
            <span>{t(locale, "language")}</span>
            <select
              value={locale}
              onChange={(event) => setLocale(event.target.value as Locale)}
            >
              <option value="vi">Tiếng Việt</option>
              <option value="en">English</option>
            </select>
          </label>
        </div>
      </header>

      <section className="workspace">
        <aside className="sidebar" aria-label="KOVA workflow">
          <p className="sidebar-title">KOVA WORKFLOW</p>
          <button
            className={`stage-nav auto-nav ${autoTabOpen ? "selected" : ""}`}
            onClick={() => {
              setAutoTabOpen(true);
              setMessage("");
            }}
          >
            <span className={`stage-dot ${automationActive ? "running" : "not_started"}`} />
            <span>
              <strong>00</strong> · {locale === "vi" ? "Tự động toàn bộ" : "Full automation"}
            </span>
            <small>
              {automationActive
                ? locale === "vi"
                  ? "Đang chạy tuần tự"
                  : "Running sequentially"
                : locale === "vi"
                  ? "Nhập URL và chạy hết"
                  : "Paste URL and run all"}
            </small>
          </button>
          <nav>
            {data.stages.map((item) => (
              <button
                className={`stage-nav ${item.id === activeStage ? "selected" : ""}`}
                key={item.id}
                onClick={() => {
                  setAutoTabOpen(false);
                  setActiveStage(item.id);
                  setDraft("");
                  setLoadedDraftKey("");
                  setWorkflowStatus(null);
                  setMessage("");
                }}
              >
                <span className={`stage-dot ${statuses[item.id]}`} />
                <span>
                  <strong>{item.number}</strong> · {stageTitle(locale, item)}
                </span>
                <small>{statusLabel(locale, statuses[item.id])}</small>
              </button>
            ))}
          </nav>
          <div className="sidebar-bottom">
            <button className="quiet-button">⚙ {t(locale, "settings")}</button>
            <button className="quiet-button">◷ {t(locale, "history")}</button>
          </div>
        </aside>

        <section className="center-pane">
          <div className="project-toolbar">
            <label>
              {t(locale, "selectProject")}
              <select
                value={snapshot?.project.id ?? ""}
                onChange={(event) =>
                  void handleSelectProject(event.target.value)
                }
              >
                <option value="" disabled>
                  {t(locale, "noProject")}
                </option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {t(locale, "newProjectName")}
              <input
                value={projectName}
                onChange={(event) => setProjectName(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void handleCreateProject();
                }}
              />
            </label>
            <button
              className="secondary"
              disabled={busy || !projectName.trim()}
              onClick={() => void handleCreateProject()}
            >
              {t(locale, "createProject")}
            </button>
			<button
			  className="danger-button"
			  disabled={busy || !snapshot}
			  onClick={() => void handleDeleteProject()}
			>
			  {locale === "vi" ? "Xóa dự án" : "Delete project"}
			</button>
          </div>
          <div className="breadcrumb">
            {autoTabOpen
              ? locale === "vi"
                ? "00 · Tự động toàn bộ"
                : "00 · Full automation"
              : `${stage?.number ?? ""} · ${stage ? stageTitle(locale, stage) : ""}`}
          </div>
          {autoTabOpen ? (
            <section className="stage-card auto-workflow-card">
              <div className="auto-workflow-heading">
                <div>
                  <h1>{locale === "vi" ? "Chạy tự động từ URL" : "Run automatically from a URL"}</h1>
                  <p>
                    {locale === "vi"
                      ? "KOVA sẽ tải video, tạo SRT, dịch, tạo giọng, ghép video và chèn phụ đề. Mỗi đầu ra được tự duyệt rồi mới chuyển sang bước kế tiếp."
                      : "KOVA downloads the video, creates subtitles, translates, dubs, assembles video, and burns captions. Each output is automatically approved before the next step starts."}
                  </p>
                </div>
                <span className={`auto-mode-badge ${automationActive ? "running" : ""}`}>
                  {automationActive
                    ? locale === "vi"
                      ? "Đang chạy"
                      : "Running"
                    : locale === "vi"
                      ? "Sẵn sàng"
                      : "Ready"}
                </span>
              </div>

              <label className="auto-url-field">
                {locale === "vi" ? "Nguồn video (URL hoặc file trên máy)" : "Video source (URL or local file)"}
                <input
                  type="text"
                  value={automationURL}
                  placeholder={locale === "vi" ? "YouTube, TikTok, Douyin hoặc chọn file video" : "YouTube, TikTok, Douyin, or choose a local video"}
                  disabled={busy || automationActive}
                  onChange={(event) => setAutomationURL(event.target.value)}
                />
              </label>
              <div className="source-picker-actions">
                <button
                  className="secondary"
                  disabled={busy || automationActive}
                  onClick={() => void handleSelectSourceVideo("automation")}
                >
                  {locale === "vi" ? "Chọn video từ máy" : "Choose local video"}
                </button>
                <span>
                  {locale === "vi" ? "Hỗ trợ YouTube, TikTok, Douyin và video MP4/MOV/MKV/WEBM/AVI." : "Supports YouTube, TikTok, Douyin, and MP4/MOV/MKV/WEBM/AVI files."}
                </span>
              </div>

              <label className="source-cookie-selector">
                {locale === "vi" ? "Phiên trình duyệt cho Douyin/TikTok" : "Browser session for Douyin/TikTok"}
                <select
                  value={sourceCookieBrowser}
                  disabled={busy || automationActive}
                  onChange={(event) => setSourceCookieBrowser(event.target.value as "auto" | "none" | "chrome" | "edge")}
                >
                  <option value="auto">{locale === "vi" ? "Tự động: phiên riêng KOVA (Edge rồi Chrome)" : "Auto: isolated KOVA session (Edge then Chrome)"}</option>
                  <option value="chrome">{locale === "vi" ? "Chrome (profile riêng KOVA, được lưu)" : "Chrome (saved isolated KOVA profile)"}</option>
                  <option value="edge">{locale === "vi" ? "Microsoft Edge (profile riêng KOVA, được lưu)" : "Microsoft Edge (saved isolated KOVA profile)"}</option>
                  <option value="none">{locale === "vi" ? "Tắt phiên trình duyệt KOVA" : "Disable the KOVA browser session"}</option>
                </select>
                <small>
                  {locale === "vi"
                    ? "Douyin được tải trong profile riêng của KOVA để chính trang web tạo chữ ký hợp lệ. Profile này không phải profile Chrome/Edge cá nhân và được giữ lại để không phải xác minh lại mỗi lần."
                    : "Douyin loads inside KOVA's isolated profile so the website creates the valid signature itself. It is separate from your personal browser and is retained to avoid repeated verification."}
                </small>
              </label>
              <div className="source-picker-actions">
                <button
                  className="secondary"
                  disabled={busy || automationActive || sourceCookieBrowser === "none"}
                  onClick={() => void handleOpenShortVideoSession(automationURL)}
                >
                  {locale === "vi" ? "Thiết lập phiên Douyin/TikTok" : "Set up Douyin/TikTok session"}
                </button>
                <span>
                  {locale === "vi"
                    ? "Chỉ cần khi nền tảng hiện đăng nhập hoặc CAPTCHA; hoàn tất một lần rồi đóng cửa sổ."
                    : "Only needed when the platform shows sign-in or CAPTCHA; complete it once, then close the window."}
                </span>
              </div>

              <div className="auto-steps" aria-label="Automatic workflow steps">
                {[
                  ["01", locale === "vi" ? "Tải video" : "Download video", "source"],
                  ["02", locale === "vi" ? "Tạo script, dịch và phụ đề" : "Create script, translate and subtitle", "translation"],
                  ["03", locale === "vi" ? "Tạo audio lồng tiếng" : "Create dubbed audio", "dubbing_audio"],
                  ["04", locale === "vi" ? "Ghép audio vào video" : "Combine audio with video", "render"],
                  ["05", locale === "vi" ? "Chèn phụ đề và xuất file" : "Burn captions and export", "outputs"],
                ].map(([number, label, id]) => {
                  const status = statuses[id as StageId];
                  return (
                    <div className="auto-step" key={id}>
                      <span className={`stage-dot ${status}`} />
                      <strong>{number}</strong>
                      <span>{label}</span>
                      <small>{statusLabel(locale, status)}</small>
                    </div>
                  );
                })}
              </div>

              <div className="auto-config-grid">
                <section className="auto-config-section">
                  <h2>01 · {locale === "vi" ? "Nguồn và tạo script" : "Source and script"}</h2>
                  <div className="source-language-grid">
                    <label>
                      {locale === "vi" ? "Ngôn ngữ đầu vào" : "Input language"}
                      <select
                        value={originLanguage}
                        disabled={busy || automationActive}
                        onChange={(event) => setOriginLanguage(event.target.value)}
                      >
                        {sourceLanguageOptions.map((language) => (
                          <option key={language.value} value={language.value}>
                            {locale === "vi" ? language.vi : language.en}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label>
                      {locale === "vi" ? "Ngôn ngữ đầu ra" : "Output language"}
                      <select
                        value={targetLanguage}
                        disabled={busy || automationActive}
                        onChange={(event) => setTargetLanguage(event.target.value)}
                      >
                        {targetLanguageOptions.map((language) => (
                          <option key={language.value} value={language.value}>
                            {locale === "vi" ? language.vi : language.en}
                          </option>
                        ))}
                      </select>
                    </label>
                  </div>
                  <label>
                    {locale === "vi" ? "Cách tạo script" : "Script method"}
                    <select
                      value={sourceMethod}
                      disabled={busy || automationActive}
                      onChange={(event) =>
                        setSourceMethod(
                          event.target.value as
                            | "speech_to_text"
                            | "visual_ocr"
                            | "speech_to_text_and_visual_ocr",
                        )
                      }
                    >
                      <option value="speech_to_text">
                        {locale === "vi" ? "Speech-to-text từ audio" : "Speech-to-text from audio"}
                      </option>
                      <option value="visual_ocr">
                        {locale === "vi" ? "OCR chữ/phụ đề có sẵn" : "OCR existing visible captions"}
                      </option>
                      <option value="speech_to_text_and_visual_ocr">
                        {locale === "vi" ? "Kết hợp STT + OCR" : "Combine STT + OCR"}
                      </option>
                    </select>
                  </label>
                  {sourceMethod !== "visual_ocr" && (
                    <>
                      <label>
                        Speech-to-text
                        <select
                          value={sttOptionID}
						  disabled={busy || automationActive}
                          onChange={(event) => {
                            setSTTOptionID(event.target.value);
                            setSTTConnectionMessage("");
                          }}
                        >
                          {sttOptions.map((option) => (
                            <option key={option.id} value={option.id}>
                              {locale === "vi" ? option.label_vi : option.label_en}
                            </option>
                          ))}
                        </select>
                      </label>
                      {selectedSTT?.needs_worker && (
                        <div className="auto-worker-fields">
                          <button
                            className="secondary"
                            disabled={busy || automationActive}
                            onClick={() => void openColabNotebook(data.stt_colab_notebook_url)}
                          >
                            {locale === "vi" ? "Mở notebook STT trên Colab" : "Open STT Colab notebook"}
                          </button>
                          <label>
                            {locale === "vi" ? "URL worker STT Colab" : "Colab STT worker URL"}
                            <input
                              placeholder="https://xxxx.trycloudflare.com"
                              value={sttWorkerURL}
                              disabled={busy || automationActive}
                              onChange={(event) => setSTTWorkerURL(event.target.value)}
                            />
                          </label>
                          <label>
                            {locale === "vi" ? "Token STT Colab" : "Colab STT token"}
                            <input
                              type="password"
                              autoComplete="off"
                              placeholder="KOVA_STT_TOKEN"
                              value={sttWorkerToken}
                              disabled={busy || automationActive}
                              onChange={(event) => setSTTWorkerToken(event.target.value)}
                            />
                          </label>
                          <button
                            className="secondary"
                            disabled={busy || automationActive || !sttWorkerURL.trim() || !sttWorkerToken.trim()}
                            onClick={() => void handleSTTConnectionCheck()}
                          >
                            {locale === "vi" ? "Kiểm tra STT" : "Check STT"}
                          </button>
                          {sttConnectionMessage && <p className="connection-message">{sttConnectionMessage}</p>}
                        </div>
                      )}
                    </>
                  )}
                  {(sourceMethod === "visual_ocr" || sourceMethod === "speech_to_text_and_visual_ocr") && (
                    <div className="auto-ocr-fields">
					  <label>
						{locale === "vi" ? "Bộ máy OCR" : "OCR engine"}
						<select value={ocrEngine} disabled={busy || automationActive} onChange={(event) => { setOCREngine(event.target.value as "colab" | "local"); setOCRConnectionMessage(""); }}>
						  <option value="colab">{locale === "vi" ? "Google Colab GPU (khuyến nghị)" : "Google Colab GPU (recommended)"}</option>
						  <option value="local">{locale === "vi" ? "OCR local (PaddleOCR)" : "Local OCR (PaddleOCR)"}</option>
						</select>
					  </label>
					  {ocrEngine === "colab" && (
						<div className="auto-worker-fields">
						  <button className="secondary" disabled={busy || automationActive} onClick={() => void openColabNotebook(data.ocr_colab_notebook_url)}>
							{locale === "vi" ? "Mở notebook OCR trên Colab" : "Open OCR Colab notebook"}
						  </button>
						  <label>{locale === "vi" ? "URL worker OCR Colab" : "Colab OCR worker URL"}<input placeholder="https://xxxx.trycloudflare.com" value={ocrWorkerURL} disabled={busy || automationActive} onChange={(event) => setOCRWorkerURL(event.target.value)} /></label>
						  <label>{locale === "vi" ? "Token OCR Colab" : "Colab OCR token"}<input type="password" autoComplete="off" placeholder="KOVA_OCR_TOKEN" value={ocrWorkerToken} disabled={busy || automationActive} onChange={(event) => setOCRWorkerToken(event.target.value)} /></label>
						  <button className="secondary" disabled={busy || automationActive || !ocrWorkerURL.trim() || !ocrWorkerToken.trim()} onClick={() => void handleOCRConnectionCheck()}>{locale === "vi" ? "Kiểm tra OCR" : "Check OCR"}</button>
						  {ocrConnectionMessage && <p className="connection-message">{ocrConnectionMessage}</p>}
						</div>
					  )}
                      <label>
                        {locale === "vi" ? "Ngôn ngữ chữ trong video" : "Visible-text language"}
                        <select
                          value={ocrLanguage}
                          disabled={busy || automationActive}
                          onChange={(event) => setOCRLanguage(event.target.value)}
                        >
                          <option value="en">English</option>
                          <option value="vi">Tiếng Việt</option>
                          <option value="ch">中文</option>
                          <option value="japan">日本語</option>
                          <option value="korean">한국어</option>
                          <option value="fr">Français</option>
                          <option value="de">Deutsch</option>
                          <option value="es">Español</option>
                        </select>
                      </label>
                      <div className="ocr-region-grid">
                        <label>X<input type="number" min="0" max="1" step="0.01" value={ocrRegionX} disabled={busy || automationActive} onChange={(event) => setOCRRegionX(event.target.value)} /></label>
                        <label>Y<input type="number" min="0" max="1" step="0.01" value={ocrRegionY} disabled={busy || automationActive} onChange={(event) => setOCRRegionY(event.target.value)} /></label>
                        <label>{locale === "vi" ? "Rộng" : "Width"}<input type="number" min="0.01" max="1" step="0.01" value={ocrRegionWidth} disabled={busy || automationActive} onChange={(event) => setOCRRegionWidth(event.target.value)} /></label>
                        <label>{locale === "vi" ? "Cao" : "Height"}<input type="number" min="0.01" max="1" step="0.01" value={ocrRegionHeight} disabled={busy || automationActive} onChange={(event) => setOCRRegionHeight(event.target.value)} /></label>
                      </div>
                      <label>
                        {locale === "vi" ? "Khoảng quét (ms)" : "Sampling interval (ms)"}
                        <input type="number" min="40" max="5000" step="10" value={ocrIntervalMS} disabled={busy || automationActive} onChange={(event) => setOCRIntervalMS(event.target.value)} />
                      </label>
                      <label className="ocr-gpu-toggle">
                        <input type="checkbox" checked={ocrPreferGPU} disabled={busy || automationActive} onChange={(event) => setOCRPreferGPU(event.target.checked)} />
                        {locale === "vi" ? "Ưu tiên GPU cho OCR nếu có" : "Prefer GPU for OCR when available"}
                      </label>
                      <div className="ocr-setup-actions">
                        <button
                          className="secondary"
						  disabled={busy || automationActive || ocrEngine !== "local"}
                          onClick={() => void handleVisualOCRSetup(false)}
                        >
                          {locale === "vi" ? "Kiểm tra OCR local" : "Check local OCR"}
                        </button>
                        <button
                          className="secondary"
                          disabled={busy || automationActive || ocrEngine !== "local"}
                          onClick={() => void handleVisualOCRSetup(true)}
                        >
                          {locale === "vi" ? "Cài OCR local" : "Install local OCR"}
                        </button>
                        {ocrHealthMessage && <span>{ocrHealthMessage}</span>}
                      </div>
                      {sourceMethod === "speech_to_text_and_visual_ocr" && (
                        <label className="ocr-gpu-toggle">
                          <input type="checkbox" checked={ocrFallbackToSTT} disabled={busy || automationActive} onChange={(event) => setOCRFallbackToSTT(event.target.checked)} />
                          {locale === "vi" ? "Nếu Visual OCR chưa sẵn sàng, tự dùng Speech-to-Text và tiếp tục luồng Auto" : "If Visual OCR is unavailable, continue the Auto workflow with Speech-to-Text"}
                        </label>
                      )}
                      {sourceMethod === "speech_to_text_and_visual_ocr" && (
                        <p className="auto-config-help">
                          {locale === "vi" ? "Khuyên dùng: OCR chỉ dùng để hiệu chỉnh chữ đã thấy. STT vẫn giữ mốc thời gian và là phương án an toàn nếu worker OCR chưa sẵn sàng." : "Recommended: OCR only corrects visible text. STT keeps the timing and is the safe fallback when the OCR worker is unavailable."}
                        </p>
                      )}
                    </div>
                  )}
                </section>

                <section className="auto-config-section">
                  <h2>02 · {locale === "vi" ? "Dịch và phụ đề" : "Translation and subtitles"}</h2>
                  <label>
                    {locale === "vi" ? "Model dịch" : "Translation model"}
                    <select
                      value={translationModelID}
                      disabled={busy || automationActive}
                      onChange={(event) => setTranslationModelID(event.target.value)}
                    >
                      {translationModels.map((model) => (
                        <option key={model.id} value={model.id}>
                          {locale === "vi" ? model.label_vi : model.label_en}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    {locale === "vi" ? "API Gateway key (dịch và Google/Edge TTS)" : "API Gateway key (translation and Google/Edge TTS)"}
                    <input
                      type="password"
                      autoComplete="off"
                      value={gatewayAPIKey}
                      placeholder={locale === "vi" ? "Dán key nếu không dùng biến môi trường" : "Paste key when not using an environment variable"}
                      disabled={busy || automationActive}
                      onChange={(event) => setGatewayAPIKey(event.target.value)}
                    />
                  </label>
                  <p className="auto-config-help">
                    {locale === "vi"
                      ? "Key này chỉ được giữ trong bộ nhớ của phiên chạy, không lưu vào dự án hay file cấu hình. Nếu để trống, KOVA dùng KOVA_API_GATEWAY_API_KEY đang có trên máy."
                      : "This key stays only in the running session and is not saved in the project or config file. Leave it blank to use KOVA_API_GATEWAY_API_KEY from this computer."}
                  </p>
                </section>

                <section className="auto-config-section">
                  <h2>03 · {locale === "vi" ? "Giọng lồng tiếng cố định" : "Fixed dubbing voice"}</h2>
                  <label>
                    {locale === "vi" ? "Bộ máy TTS" : "TTS engine"}
                    <select
                      value={ttsOptionID}
                      disabled={busy || automationActive}
                      onChange={(event) => {
                        setTTSOptionID(event.target.value);
                        setConnectionMessage("");
                      }}
                    >
                      {ttsOptions.map((option) => (
                        <option key={option.id} value={option.id}>
                          {locale === "vi" ? option.label_vi : option.label_en}
                        </option>
                      ))}
                    </select>
                  </label>
                  {selectedTTS?.needs_worker && (
                    <div className="auto-worker-fields">
                      <button className="secondary" disabled={busy || automationActive} onClick={() => void openColabNotebook(data.colab_notebook_url)}>
                        {locale === "vi" ? "Mở notebook Voice Studio trên Colab" : "Open Voice Studio Colab notebook"}
                      </button>
                      <label>
                        {locale === "vi" ? "URL worker Voice Studio" : "Voice Studio worker URL"}
                        <input placeholder="https://xxxx.trycloudflare.com" value={workerUrl} disabled={busy || automationActive} onChange={(event) => setWorkerUrl(event.target.value)} />
                      </label>
                      <label>
                        {locale === "vi" ? "Token Voice Studio Colab" : "Voice Studio Colab token"}
                        <input type="password" autoComplete="off" value={workerToken} disabled={busy || automationActive} onChange={(event) => setWorkerToken(event.target.value)} />
                      </label>
                      <div className="auto-worker-actions">
                        <button className="secondary" disabled={busy || automationActive || !workerUrl.trim() || !workerToken.trim()} onClick={() => void handleConnectionCheck()}>
                          {locale === "vi" ? "Kiểm tra Voice Studio" : "Check Voice Studio"}
                        </button>
                        <button className="secondary" disabled={busy || automationActive} onClick={() => void handleLoadProfiles()}>
						  {locale === "vi" ? "Tải thư viện / profile giọng" : "Load voice library / profiles"}
                        </button>
                      </div>
                    </div>
                  )}
                  {selectedTTS?.needs_profile && (
                    <label>
                      {locale === "vi" ? "Profile giọng cố định" : "Fixed voice profile"}
                      <select value={voiceProfileID} disabled={busy || automationActive} onChange={(event) => handleVoiceProfileSelection(event.target.value)}>
                        <option value="">{locale === "vi" ? "Chưa chọn profile" : "No profile selected"}</option>
                        {voiceProfiles.map((profile) => (
                          <option key={profile.id} value={profile.id}>{profile.name} · {profile.language}</option>
                        ))}
                      </select>
                    </label>
                  )}
                  {voiceProfileCreator}
                  {connectionMessage && <p className="connection-message">{connectionMessage}</p>}
                </section>

                <section className="auto-config-section">
                  <h2>04–05 · {locale === "vi" ? "Video cuối và xuất file" : "Final video and export"}</h2>
                  <label className="ocr-gpu-toggle">
                    <input type="checkbox" checked={blurOriginalText} disabled={busy || automationActive} onChange={(event) => setBlurOriginalText(event.target.checked)} />
                    {locale === "vi" ? "Che mờ vùng chữ/phụ đề cũ rồi chèn phụ đề Việt" : "Blur old text/captions, then burn Vietnamese subtitles"}
                  </label>
                  {blurOriginalText && (
                    <>
                      <div className="ocr-region-grid">
                        <label>X<input type="number" min="0" max="1" step="0.01" value={blurRegionX} disabled={busy || automationActive} onChange={(event) => setBlurRegionX(event.target.value)} /></label>
                        <label>Y<input type="number" min="0" max="1" step="0.01" value={blurRegionY} disabled={busy || automationActive} onChange={(event) => setBlurRegionY(event.target.value)} /></label>
                        <label>{locale === "vi" ? "Rộng" : "Width"}<input type="number" min="0.01" max="1" step="0.01" value={blurRegionWidth} disabled={busy || automationActive} onChange={(event) => setBlurRegionWidth(event.target.value)} /></label>
                        <label>{locale === "vi" ? "Cao" : "Height"}<input type="number" min="0.01" max="1" step="0.01" value={blurRegionHeight} disabled={busy || automationActive} onChange={(event) => setBlurRegionHeight(event.target.value)} /></label>
                      </div>
                      <label>
                        {locale === "vi" ? "Độ mạnh che mờ (1–11)" : "Blur strength (1–11)"}
                        <input type="number" min="1" max="11" step="1" value={blurStrength} disabled={busy || automationActive} onChange={(event) => setBlurStrength(event.target.value)} />
                      </label>
                    </>
                  )}
                  <p className="auto-config-help">
                    {locale === "vi" ? "Luồng này tự chèn phụ đề tiếng Việt và tạo video MP4 cuối cùng. Sau khi xong, bạn có thể xem trước, tải file hoặc mở thư mục ở bước 05." : "This workflow burns Vietnamese subtitles and creates the final MP4. When it finishes, you can preview, download, or open its folder in step 05."}
                  </p>
                </section>
              </div>

              {automationActive && workflowStatus && (
                <section className="workflow-status auto-workflow-status">
                  <div className="workflow-status-heading">
                    <strong>{locale === "vi" ? "Tiến độ luồng tự động" : "Automatic workflow progress"}</strong>
                    <span>{workflowStatus.current_stage}</span>
                  </div>
                  <div className="workflow-progress-label">
                    <span>{workflowStatus.message || (locale === "vi" ? "Đang chờ worker cập nhật" : "Waiting for worker update")}</span>
                    <strong>{safePercent(workflowStatus.process_percent)}%</strong>
                  </div>
                  <div className="workflow-progress-track">
                    <i style={{ width: `${safePercent(workflowStatus.process_percent)}%` }} />
                  </div>
                  <p className="workflow-poll-note">
                    {workflowStatus.completed_at
                      ? `${locale === "vi" ? "Hoàn tất lúc" : "Completed at"}: ${formatRunTime(locale, workflowStatus.completed_at)}`
                      : `${locale === "vi" ? "Bắt đầu" : "Started"}: ${formatRunTime(locale, activeRun?.created_at)} · ${locale === "vi" ? "Đã chạy" : "Elapsed"}: ${formatElapsed(locale, activeRun?.created_at, now)}`}
                  </p>
                  {workflowStatus.source_warning && (
                    <p className="workflow-warning" role="status">⚠ {workflowStatus.source_warning}</p>
                  )}
                </section>
              )}

              <p className="auto-workflow-note">
                {locale === "vi"
                  ? "Bạn có thể cấu hình toàn bộ ngay trong tab này; các lựa chọn cũng được đồng bộ sang các tab chi tiết 01–05. Luồng này luôn tự duyệt đầu ra. Giữ KOVA mở trong lúc chạy; URL và token Colab/API chỉ lưu trong phiên hiện tại."
                  : "You can configure everything in this tab; the same choices are synchronized with detailed tabs 01–05. This flow always auto-approves outputs. Keep KOVA open while it runs; Colab/API URLs and tokens stay only in this session."}
              </p>
              {message && <p className="error-message">{message}</p>}
              <footer className="stage-actions auto-workflow-actions">
                <button
                  className="secondary"
                  disabled={busy || automationActive}
                  onClick={() => {
                    setAutoTabOpen(false);
                    setActiveStage("source");
                  }}
                >
                  {locale === "vi" ? "Xem các tab chi tiết" : "View detailed tabs"}
                </button>
                <button
                  className="primary"
                  disabled={busy || automationActive || !automationURL.trim()}
                  onClick={() => void handleStartAutomation()}
                >
                  {locale === "vi" ? "Bắt đầu tự động" : "Start automatically"}
                </button>
              </footer>
            </section>
          ) : (
            <>
          <div className="preview-card">
            {previewURL ? (
              <div className="preview-media" ref={blurPreviewRef}>
                <video
                  className="preview-video"
                  key={previewURL}
                  src={previewURL}
                  controls
                  preload="metadata"
                  onLoadedMetadata={(event) => {
                    setPreviewDuration(event.currentTarget.duration);
                    setPreviewTime(event.currentTarget.currentTime);
                    setPreviewError("");
                  }}
                  onTimeUpdate={(event) =>
                    setPreviewTime(event.currentTarget.currentTime)
                  }
                  onError={() =>
                    setPreviewError(
                      locale === "vi"
                        ? "Không thể phát video nguồn. Hãy mở artifact từ worker để tải file hoặc kiểm tra lại worker."
                        : "The source video could not be played. Open the worker artifact to download it or check the worker.",
                    )
                  }
                />
                {activeStage === "outputs" && blurOriginalText && !previewError && (
                  <div
                    className={`blur-selection ${blurDrag ? "dragging" : ""}`}
                    style={{
                      left: `${previewBlurRect.x * 100}%`,
                      top: `${previewBlurRect.y * 100}%`,
                      width: `${previewBlurRect.width * 100}%`,
                      height: `${previewBlurRect.height * 100}%`,
                    }}
                    onPointerDown={(event) => startBlurDrag(event, "move")}
                    onPointerMove={updateBlurDrag}
                    onPointerUp={finishBlurDrag}
                    onPointerCancel={finishBlurDrag}
                    role="group"
                    aria-label={
                      locale === "vi"
                        ? "Vùng che mờ có thể kéo và thay đổi kích thước"
                        : "Draggable and resizable blur region"
                    }
                  >
                    <span className="blur-selection-label">
                      {locale === "vi"
                        ? "Kéo để đặt vùng che chữ cũ"
                        : "Drag to position old-text mask"}
                    </span>
                    <button
                      type="button"
                      className="blur-resize-handle"
                      aria-label={
                        locale === "vi"
                          ? "Kéo để đổi kích thước vùng che mờ"
                          : "Drag to resize blur region"
                      }
                      onPointerDown={(event) => startBlurDrag(event, "resize")}
                      onPointerMove={updateBlurDrag}
                      onPointerUp={finishBlurDrag}
                      onPointerCancel={finishBlurDrag}
                    />
                  </div>
                )}
                {previewError && (
                  <p className="preview-error">{previewError}</p>
                )}
              </div>
            ) : (
              <div className="preview-placeholder">
                <span>▶</span>
                <p>
                  {snapshot
                    ? locale === "vi"
                      ? "Chưa có video từ worker. Video sẽ hiện ở đây ngay khi artifact nguồn được tạo."
                      : "No worker video yet. The source artifact will appear here as soon as it is created."
                    : t(locale, "noProject")}
                </p>
              </div>
            )}
            <div className="timeline">
              <span>{formatMediaTime(previewTime)}</span>
              <div className="timeline-line">
                <i style={{ width: `${previewPercent}%` }} />
              </div>
              <span>{formatMediaTime(previewDuration)}</span>
            </div>
			{finalVideo && (
			  <section className="final-video-panel" aria-label={locale === "vi" ? "Video hoàn chỉnh" : "Completed video"}>
				<div>
				  <strong>{locale === "vi" ? "Video hoàn chỉnh" : "Completed video"}</strong>
				  <p>{finalVideo.name} · {formatFileSize(finalVideo.size_bytes)}</p>
				  <code title={finalVideo.path}>{finalVideo.path}</code>
				</div>
				<div className="final-video-actions">
				  <button
					type="button"
					className="secondary"
					disabled={busy || !snapshot}
					onClick={() => void withBusy(async () => {
					  if (!snapshot) return;
					  await revealDesktopWorkflowFinalVideo(snapshot.project.id);
					  setMessage(locale === "vi" ? "Đã mở thư mục chứa video hoàn chỉnh." : "Opened the completed-video folder.");
					})}
				  >
					{locale === "vi" ? "Mở thư mục" : "Open folder"}
				  </button>
				  <button
					type="button"
					className="primary"
					disabled={busy || !snapshot}
					onClick={() => void withBusy(async () => {
					  if (!snapshot) return;
					  const savedPath = await saveDesktopWorkflowFinalVideo(snapshot.project.id);
					  if (savedPath) setMessage(locale === "vi" ? `Đã lưu video: ${savedPath}` : `Saved video: ${savedPath}`);
					})}
				  >
					{locale === "vi" ? "Lưu thành…" : "Save as…"}
				  </button>
				  {finalVideoURL && (
					<a className="secondary final-video-link" href={finalVideoURL} target="_blank" rel="noreferrer">
					  {locale === "vi" ? "Tải file" : "Download file"}
					</a>
				  )}
				  <button
					type="button"
					className="secondary"
					onClick={() => setPreviewReloadToken((value) => value + 1)}
				  >
					{locale === "vi" ? "Tải lại xem trước" : "Reload preview"}
				  </button>
				</div>
			  </section>
			)}
          </div>
          <section className="stage-card">
            <h1>{stage ? stageTitle(locale, stage) : data.name}</h1>
            <p>{t(locale, hintKey(activeStage))}</p>
            {activeRun && (
              <section
                className={`workflow-status ${activeRun.status}`}
                aria-live="polite"
              >
                <div className="workflow-status-heading">
                  <strong>
                    {locale === "vi" ? "Theo dõi tác vụ" : "Task tracking"}
                  </strong>
                  <span>{statusLabel(locale, activeRun.status)}</span>
                </div>
                <div className="workflow-progress-label">
                  <span>
                    {activeStage === "source"
                      ? locale === "vi"
                        ? "Tổng tiến độ nguồn"
                        : "Overall source progress"
						: activeStage === "translation"
							? locale === "vi" ? "Tiến độ dịch" : "Translation progress"
                      : locale === "vi"
                        ? "Tiến độ worker"
                        : "Worker progress"}
                  </span>
                  <strong>{workflowPercent}%</strong>
                </div>
                <div
                  className="workflow-progress-track"
                  role="progressbar"
                  aria-label={
                    activeStage === "source"
                      ? locale === "vi"
                        ? "Tổng tiến độ nguồn"
                        : "Overall source progress"
						: activeStage === "translation"
							? locale === "vi" ? "Tiến độ dịch" : "Translation progress"
                      : locale === "vi"
                        ? "Tiến độ worker"
                        : "Worker progress"
                  }
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={workflowPercent}
                >
                  <i style={{ width: `${workflowPercent}%` }} />
                </div>
                {sourceSteps.length > 0 && (
                  <section
                    className="source-step-list"
                    aria-label={
                      activeStage === "render"
                        ? workflowStepsHeading
						: activeStage === "translation"
						? locale === "vi"
							? "Các bước dịch và tạo phụ đề"
							: "Translation and subtitle steps"
						: activeStage === "dubbing_audio"
                        ? locale === "vi"
                          ? "Các bước tạo audio lồng tiếng"
                          : "Dubbed-audio processing steps"
                        : locale === "vi"
                          ? "Các bước xử lý nguồn"
                          : "Source processing steps"
                    }
                  >
                    <h2>
                      {activeStage === "render"
                        ? workflowStepsHeading
						: activeStage === "translation"
						? locale === "vi"
							? "Các bước dịch và tạo phụ đề"
							: "Translation and subtitle steps"
						: activeStage === "dubbing_audio"
                        ? locale === "vi"
                          ? "Các bước tạo audio lồng tiếng"
                          : "Dubbed-audio processing steps"
                        : locale === "vi"
                          ? "Các bước xử lý nguồn"
                          : "Source processing steps"}
                    </h2>
                    {sourceSteps.map((step) => {
                      const percent =
                        step.state === "completed"
                          ? 100
                          : safePercent(step.percent);
                      return (
                        <div
                          key={step.id}
                          className={`source-step ${step.state}`}
                        >
                          <div className="source-step-heading">
                            <strong>
                              {workflowStepTitle(locale, step.id)}
                            </strong>
                            <span>
                              {sourceStepStateLabel(locale, step.state)}
                            </span>
                          </div>
                          <div className="source-step-progress-label">
                            <span>{percent}%</span>
                          </div>
                          <div
                            className="source-step-track"
                            role="progressbar"
                            aria-label={workflowStepTitle(locale, step.id)}
                            aria-valuemin={0}
                            aria-valuemax={100}
                            aria-valuenow={percent}
                          >
                            <i style={{ width: `${percent}%` }} />
                          </div>
                          {step.detail && <small>{step.detail}</small>}
                        </div>
                      );
                    })}
                  </section>
                )}
                <dl className="workflow-metadata">
                  <div>
                    <dt>{locale === "vi" ? "Bắt đầu" : "Started"}</dt>
                    <dd>{formatRunTime(locale, activeRun.created_at)}</dd>
                  </div>
                  <div>
                    <dt>{locale === "vi" ? "Đã chạy" : "Elapsed"}</dt>
                    <dd>{formatElapsed(locale, activeRun.created_at, now)}</dd>
                  </div>
				  {workflowStatus?.completed_at ? (
					<div>
					  <dt>{locale === "vi" ? "Hoàn tất thực tế" : "Completed"}</dt>
					  <dd>{formatRunTime(locale, workflowStatus.completed_at)}</dd>
					</div>
				  ) : workflowStatus?.estimated_completion_at ? (
					<div>
					  <dt>{locale === "vi" ? "Dự kiến hoàn tất" : "Estimated finish"}</dt>
					  <dd>{formatRunTime(locale, workflowStatus.estimated_completion_at)}</dd>
					</div>
				  ) : null}
                  <div>
                    <dt>
                      {locale === "vi" ? "Cập nhật gần nhất" : "Last update"}
                    </dt>
                    <dd>
                      {formatRunTime(
                        locale,
                        workflowStatus?.updated_at || activeRun.updated_at,
                      )}
                    </dd>
                  </div>
                  {snapshot?.project.workflow_task_id && (
                    <div>
                      <dt>{locale === "vi" ? "Mã job" : "Job ID"}</dt>
                      <dd>
                        <code>{snapshot.project.workflow_task_id}</code>
                      </dd>
                    </div>
                  )}
                </dl>
                {activeRun.status === "running" && (
                  <p className="workflow-poll-note">
                    {locale === "vi"
                      ? "Tự cập nhật mỗi 4 giây. Thời gian hoàn tất chỉ là ước lượng của worker nên KOVA hiển thị thời gian đã chạy và tiến độ thực tế."
                      : "Updates automatically every 4 seconds. Completion time is worker-dependent, so KOVA shows actual elapsed time and progress."}
                  </p>
                )}
				{workflowStatus?.review_mode === "auto" && (
					<p className="workflow-poll-note">
						{locale === "vi"
							? "Chế độ Auto đang bật: output hoàn tất sẽ được lưu và tự duyệt; bạn vẫn có thể mở artifact ở cột bên phải."
							: "Auto mode is on: completed output is saved and auto-approved; you can still open every artifact on the right."}
					</p>
				)}
                {workflowStatus?.message && (
                  <p className="workflow-worker-message">
                    {workflowStatus.message}
                  </p>
                )}
                {failureDetail && (
                  <p className="workflow-failure">
                    <strong>
                      {locale === "vi" ? "Lỗi chi tiết:" : "Detailed error:"}
                    </strong>{" "}
                    {failureDetail}
                  </p>
                )}
              </section>
            )}
            {activeStage === "translation" && (
              <div className="worker-form">
                <label>
                  {locale === "vi"
                    ? "Model dịch miễn phí"
                    : "Free translation model"}
                  <select
                    value={translationModelID}
                    onChange={(event) =>
                      setTranslationModelID(event.target.value)
                    }
                  >
                    {translationModels.map((model) => (
                      <option key={model.id} value={model.id}>
                        {locale === "vi" ? model.label_vi : model.label_en}
                      </option>
                    ))}
                  </select>
                </label>
                <p className="worker-help">
                  {locale === "vi"
                    ? "KOVA chỉ gửi các model free đã kiểm chứng qua API Gateway trong danh sách này."
                    : "KOVA sends only the verified free gateway models listed here."}
                </p>
              </div>
            )}
            {activeStage === "translation" &&
              translationWarnings.length > 0 && (
                <section
                  className="translation-warning-panel"
                  aria-live="polite"
                >
                  <h2>
                    {locale === "vi"
                      ? "Cảnh báo từ nghi là tiếng Anh — không chặn duyệt"
                      : "Possible English words — approval is not blocked"}
                  </h2>
                  <p>
                    {locale === "vi"
                      ? "Bản dịch đã được tạo. Kiểm tra các cue bên dưới, sửa SRT nếu cần; nếu các từ là tên riêng/thuật ngữ hợp lệ, bạn có thể bấm Duyệt đầu ra để tiếp tục."
                      : "The translation is ready. Review the cues below and edit the SRT if needed. If they are valid names or terms, you may approve the output and continue."}
                  </p>
                  <ul>
                    {translationWarnings.map((warning) => {
						const suspiciousWords = Array.isArray(warning.suspicious_words)
							? warning.suspicious_words.filter((word): word is string => typeof word === "string" && word.trim().length > 0)
							: [];
                      const missingModelOutput =
                        warning.reason === "model_empty";
                      const label = missingModelOutput
                        ? locale === "vi"
                          ? "Chưa có bản dịch"
                          : "No model translation"
						: suspiciousWords.length > 0
							? suspiciousWords.join(", ")
							: locale === "vi"
								? "Cần kiểm tra thủ công"
								: "Manual review needed";
                      const detail = missingModelOutput
                        ? locale === "vi"
                          ? `Model không trả nội dung; cue được giữ nguyên để bạn dịch/sửa: ${warning.text}`
                          : `The model returned no text; the source cue was kept for you to translate/edit: ${warning.text}`
                        : warning.text;
                      return (
                        <li
							key={`${warning.cue_index}-${warning.reason ?? ""}-${suspiciousWords.join("-")}`}
                        >
                          <strong>
                            {locale === "vi"
                              ? `Cue ${warning.cue_index}`
                              : `Cue ${warning.cue_index}`}
                          </strong>
                          <span>{label}</span>
                          <small>{detail}</small>
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}
            {activeStage === "dubbing_audio" && (
              <div className="worker-form">
                <label>
                  {t(locale, "ttsProvider")}
                  <select
                    value={ttsOptionID}
                    onChange={(event) => setTTSOptionID(event.target.value)}
                  >
                    {ttsOptions.map((option) => (
                      <option key={option.id} value={option.id}>
                        {locale === "vi" ? option.label_vi : option.label_en}
                      </option>
                    ))}
                  </select>
                </label>
                {selectedTTS?.needs_profile && (
                  <label>
                    {t(locale, "fixedVoice")}
                    <select
                      value={voiceProfileID}
                      onChange={(event) =>
                        handleVoiceProfileSelection(event.target.value)
                      }
                    >
                      <option value="">{t(locale, "noProfile")}</option>
                      {voiceProfiles.map((profile) => (
                        <option key={profile.id} value={profile.id}>
                          {profile.name} · {profile.language}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                {selectedTTS?.needs_worker && (
                  <>
                    <button
                      className="secondary"
                      onClick={() =>
                        void openColabNotebook(data.colab_notebook_url)
                      }
                    >
                      {t(locale, "openColab")}
                    </button>
                    <label>
                      {t(locale, "colabUrl")}
                      <input
                        placeholder="https://xxxx.trycloudflare.com"
                        value={workerUrl}
                        onChange={(event) => setWorkerUrl(event.target.value)}
                      />
                    </label>
                    <label>
                      {t(locale, "colabToken")}
                      <input
                        type="password"
                        autoComplete="off"
                        value={workerToken}
                        onChange={(event) => setWorkerToken(event.target.value)}
                      />
                    </label>
                    <button
                      className="secondary"
                      disabled={busy || !workerUrl.trim()}
                      onClick={() => void handleConnectionCheck()}
                    >
                      {t(locale, "checkConnection")}
                    </button>
                    <button
                      className="secondary"
                      disabled={busy || !workerUrl.trim()}
                      onClick={() => void handleLoadProfiles()}
                    >
                      {t(locale, "loadProfiles")}
                    </button>
                  </>
                )}
                {selectedTTS?.needs_profile && (
                  <p className="worker-help">{t(locale, "profileHelp")}</p>
                )}
                {voiceProfileCreator}
                {connectionMessage && (
                  <p className="connection-message">{connectionMessage}</p>
                )}
              </div>
            )}
            {activeStage === "dubbing_audio" && dubbedAudioURL && (
              <section className="audio-review-panel" aria-label="Dubbed audio review">
                <div className="audio-review-heading">
                  <div>
                    <h2>
                      {locale === "vi"
                        ? "Nghe và duyệt audio lồng tiếng"
                        : "Listen to and review the dubbed audio"}
                    </h2>
                    <p>
                      {locale === "vi"
                        ? "Nghe toàn bộ audio trước khi duyệt. Có thể tạo lại audio với bộ máy TTS đang chọn; video chỉ được ghép sau khi bạn duyệt."
                        : "Listen to the complete audio before approval. You may create it again with the selected TTS engine; video is not muxed until you approve."}
                    </p>
                  </div>
                  <a
                    className="secondary audio-download"
                    href={dubbedAudioURL}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {locale === "vi" ? "Mở / tải audio" : "Open / download audio"}
                  </a>
                </div>
                <audio controls preload="metadata" src={dubbedAudioURL}>
                  {locale === "vi"
                    ? "Trình duyệt không hỗ trợ phát audio. Hãy dùng nút mở/tải audio."
                    : "Your browser does not support audio playback. Use the open/download button."}
                </audio>
                <div className="audio-review-actions">
                        <button
                          className="secondary"
						  disabled={busy || activeRun?.status === "running" || ocrEngine !== "local"}
                    onClick={() => void handleStartStage()}
                  >
                    {locale === "vi" ? "Tạo lại audio" : "Create audio again"}
                  </button>
                  <span>
                    {locale === "vi"
                      ? "Nếu ổn, bấm “Duyệt đầu ra” để chuyển sang ghép video."
                      : "If it sounds right, use “Approve output” to continue to video muxing."}
                  </span>
                </div>
              </section>
            )}
            {activeStage === "translation" && (
              <div className="worker-form source-stt-form">
                <label>
                  {locale === "vi"
                    ? "Cách tạo script nguồn"
                    : "Source script method"}
                  <select
                    value={sourceMethod}
                    disabled={busy || activeRun?.status === "running"}
                    onChange={(event) => {
                      setSourceMethod(
                        event.target.value as "speech_to_text" | "visual_ocr" | "speech_to_text_and_visual_ocr",
                      );
                      setSTTConnectionMessage("");
                    }}
                  >
                    <option value="speech_to_text">
                      {locale === "vi"
                        ? "Speech-to-text từ audio"
                        : "Speech-to-text from audio"}
                    </option>
                    <option value="visual_ocr">
                      {locale === "vi"
                        ? "OCR phụ đề hiển thị trong video"
                        : "OCR visible captions in video"}
                    </option>
					<option value="speech_to_text_and_visual_ocr">
						{locale === "vi"
							? "Kết hợp STT + OCR (khuyến nghị khi video có chữ sẵn)"
							: "Combine STT + OCR (for videos with visible text)"}
					</option>
                  </select>
                </label>
				<label>
					{locale === "vi" ? "Chế độ sau khi có đầu ra" : "After-output mode"}
					<select value={reviewMode} disabled={busy || activeRun?.status === "running"} onChange={(event) => setReviewMode(event.target.value as "manual" | "auto")}>
						<option value="manual">{locale === "vi" ? "Duyệt từng bước (có thể sửa SRT/audio/video)" : "Review every step (editable outputs)"}</option>
						<option value="auto">{locale === "vi" ? "Tự động duyệt khi hoàn tất" : "Auto-approve when complete"}</option>
					</select>
				</label>
				<p className="worker-help">
					{reviewMode === "auto"
						? locale === "vi" ? "KOVA vẫn lưu tất cả artifact, nhưng tự duyệt chúng để bạn chỉ cần bấm Bắt đầu ở bước kế tiếp." : "KOVA still saves every artifact, but automatically approves it so the next stage can start without a review click."
						: locale === "vi" ? "Mỗi artifact sẽ dừng ở trạng thái cần duyệt để bạn nghe, xem và sửa trước khi tiếp tục." : "Each artifact pauses for your review so you can listen, preview, and edit before continuing."}
				</p>
				<div className="source-language-grid">
					<label>
						{locale === "vi" ? "Ngôn ngữ đầu vào" : "Input language"}
						<select
							value={originLanguage}
							disabled={busy || activeRun?.status === "running"}
							onChange={(event) => setOriginLanguage(event.target.value)}
						>
							{sourceLanguageOptions.map((language) => (
								<option key={language.value} value={language.value}>
									{locale === "vi" ? language.vi : language.en}
								</option>
							))}
						</select>
					</label>
					<label>
						{locale === "vi" ? "Ngôn ngữ đầu ra" : "Output language"}
						<select
							value={targetLanguage}
							disabled={busy || activeRun?.status === "running"}
							onChange={(event) => setTargetLanguage(event.target.value)}
						>
							{targetLanguageOptions.map((language) => (
								<option key={language.value} value={language.value}>
									{locale === "vi" ? language.vi : language.en}
								</option>
							))}
						</select>
					</label>
				</div>
				{sourceMethod !== "visual_ocr" && (
                  <>
                    <label>
                      {locale === "vi" ? "Speech-to-text" : "Speech-to-text"}
                      <select
                        value={sttOptionID}
											disabled={busy || activeRun?.status === "running"}
                        onChange={(event) => {
                          setSTTOptionID(event.target.value);
                          setSTTConnectionMessage("");
                        }}
                      >
                        {sttOptions.map((option) => (
                          <option key={option.id} value={option.id}>
                            {locale === "vi"
                              ? option.label_vi
                              : option.label_en}
                          </option>
                        ))}
                      </select>
                    </label>
                    {selectedSTT?.needs_worker ? (
                      <>
                        <button
                          className="secondary"
                          disabled={busy || activeRun?.status === "running"}
                          onClick={() =>
                            void openColabNotebook(data.stt_colab_notebook_url)
                          }
                        >
                          {locale === "vi"
                            ? "Mở notebook STT trên Google Colab"
                            : "Open STT notebook in Google Colab"}
                        </button>
                        <label>
                          {locale === "vi"
                            ? "URL worker STT Colab"
                            : "Colab STT worker URL"}
                          <input
                            placeholder="https://xxxx.trycloudflare.com"
                            value={sttWorkerURL}
                            onChange={(event) =>
                              setSTTWorkerURL(event.target.value)
                            }
                          />
                        </label>
                        <label>
                          {locale === "vi"
                            ? "Token STT Colab"
                            : "Colab STT token"}
                          <input
                            type="password"
                            autoComplete="off"
                            placeholder={
                              locale === "vi"
                                ? "Dán KOVA_STT_TOKEN do notebook in ra"
                                : "Paste KOVA_STT_TOKEN printed by the notebook"
                            }
                            value={sttWorkerToken}
                            onChange={(event) =>
                              setSTTWorkerToken(event.target.value)
                            }
                          />
                        </label>
                        <button
                          className="secondary"
                          disabled={
                            busy ||
                            !sttWorkerURL.trim() ||
                            !sttWorkerToken.trim()
                          }
                          onClick={() => void handleSTTConnectionCheck()}
                        >
                          {locale === "vi"
                            ? "Kiểm tra kết nối STT"
                            : "Check STT connection"}
                        </button>
                        <p className="worker-help">
                          {locale === "vi"
                            ? "Ấn mở notebook, chọn GPU trong Colab rồi Run all. Notebook in URL và token tạm thời; dán vào đây. Sau đó audio được gửi theo từng đoạn sang GPU Colab để tạo SRT, không dùng API Gateway hay phụ đề YouTube."
                            : "Open the notebook, choose a GPU runtime in Colab, then Run all. Paste its temporary URL and token here. Audio is sent in timed segments to Colab GPU to create the SRT; no API Gateway or YouTube captions are used."}
                        </p>
                        {sttConnectionMessage && (
                          <p className="connection-message">
                            {sttConnectionMessage}
                          </p>
                        )}
                      </>
				) : (
                      <p className="worker-help">
                        {locale === "vi"
                          ? "STT cục bộ dùng CPU/GPU của máy. Lần đầu KOVA tải engine và model đã chọn từ nguồn phát hành công khai."
                          : "Local STT uses this computer’s CPU/GPU. On first use, KOVA downloads the engine and selected model from its public release."}
                      </p>
                    )}
                  </>
                )}
				{(sourceMethod === "visual_ocr" || sourceMethod === "speech_to_text_and_visual_ocr") && (
                  <>
					<label>
					  {locale === "vi" ? "Bộ máy OCR" : "OCR engine"}
					  <select value={ocrEngine} onChange={(event) => { setOCREngine(event.target.value as "colab" | "local"); setOCRConnectionMessage(""); }}>
						<option value="colab">{locale === "vi" ? "Google Colab GPU (khuyến nghị)" : "Google Colab GPU (recommended)"}</option>
						<option value="local">{locale === "vi" ? "OCR local (PaddleOCR)" : "Local OCR (PaddleOCR)"}</option>
					  </select>
					</label>
					{ocrEngine === "colab" && (
					  <div className="auto-worker-fields">
						<button className="secondary" disabled={busy || activeRun?.status === "running"} onClick={() => void openColabNotebook(data.ocr_colab_notebook_url)}>{locale === "vi" ? "Mở notebook OCR trên Google Colab" : "Open OCR Google Colab notebook"}</button>
						<label>{locale === "vi" ? "URL worker OCR Colab" : "Colab OCR worker URL"}<input placeholder="https://xxxx.trycloudflare.com" value={ocrWorkerURL} onChange={(event) => setOCRWorkerURL(event.target.value)} /></label>
						<label>{locale === "vi" ? "Token OCR Colab" : "Colab OCR token"}<input type="password" autoComplete="off" placeholder="KOVA_OCR_TOKEN" value={ocrWorkerToken} onChange={(event) => setOCRWorkerToken(event.target.value)} /></label>
						<button className="secondary" disabled={busy || activeRun?.status === "running" || !ocrWorkerURL.trim() || !ocrWorkerToken.trim()} onClick={() => void handleOCRConnectionCheck()}>{locale === "vi" ? "Kiểm tra kết nối OCR" : "Check OCR connection"}</button>
						<p className="worker-help">{locale === "vi" ? "OCR dùng notebook Colab riêng với STT. KOVA tải video về trước, sau đó gửi video và vùng quét cho GPU OCR để tạo SRT." : "OCR uses its own Colab notebook, separate from STT. KOVA downloads the video first, then sends the video and selected region to the OCR GPU to create SRT."}</p>
						{ocrConnectionMessage && <p className="connection-message">{ocrConnectionMessage}</p>}
					  </div>
					)}
                    <label>
                      {locale === "vi"
                        ? "Ngôn ngữ chữ trong video"
                        : "Visible-caption language"}
                      <select
                        value={ocrLanguage}
                        onChange={(event) => setOCRLanguage(event.target.value)}
                      >
                        <option value="en">English</option>
                        <option value="vi">Tiếng Việt</option>
                        <option value="ch">中文</option>
                        <option value="japan">日本語</option>
                        <option value="korean">한국어</option>
                        <option value="fr">Français</option>
                        <option value="de">Deutsch</option>
                        <option value="es">Español</option>
                        <option value="ru">Русский</option>
                      </select>
                    </label>
                    <div className="ocr-region-grid">
                      <label>
                        X
                        <input
                          type="number"
                          min="0"
                          max="1"
                          step="0.01"
                          value={ocrRegionX}
                          onChange={(event) =>
                            setOCRRegionX(event.target.value)
                          }
                        />
                      </label>
                      <label>
                        Y
                        <input
                          type="number"
                          min="0"
                          max="1"
                          step="0.01"
                          value={ocrRegionY}
                          onChange={(event) =>
                            setOCRRegionY(event.target.value)
                          }
                        />
                      </label>
                      <label>
                        {locale === "vi" ? "Rộng" : "Width"}
                        <input
                          type="number"
                          min="0.01"
                          max="1"
                          step="0.01"
                          value={ocrRegionWidth}
                          onChange={(event) =>
                            setOCRRegionWidth(event.target.value)
                          }
                        />
                      </label>
                      <label>
                        {locale === "vi" ? "Cao" : "Height"}
                        <input
                          type="number"
                          min="0.01"
                          max="1"
                          step="0.01"
                          value={ocrRegionHeight}
                          onChange={(event) =>
                            setOCRRegionHeight(event.target.value)
                          }
                        />
                      </label>
                    </div>
                    <label>
                      {locale === "vi"
                        ? "Khoảng quét (ms)"
                        : "Sampling interval (ms)"}
                      <input
                        type="number"
                        min="40"
                        max="5000"
                        step="10"
                        value={ocrIntervalMS}
                        onChange={(event) =>
                          setOCRIntervalMS(event.target.value)
                        }
                      />
                    </label>
                    <label className="ocr-gpu-toggle">
                      <input
                        type="checkbox"
                        checked={ocrPreferGPU}
                        onChange={(event) =>
                          setOCRPreferGPU(event.target.checked)
                        }
                      />
                      {locale === "vi"
                        ? "Ưu tiên CUDA, tự chuyển CPU khi GPU không sẵn sàng"
                        : "Prefer CUDA and fall back to CPU if needed"}
                    </label>
                    <div className="ocr-setup-actions">
                      <button
                        className="secondary"
										disabled={busy || activeRun?.status === "running" || ocrEngine !== "local"}
                        onClick={() => void handleVisualOCRSetup(false)}
                      >
                        {locale === "vi" ? "Kiểm tra OCR local" : "Check local OCR"}
                      </button>
                      <button
                        className="secondary"
                        disabled={busy || activeRun?.status === "running" || ocrEngine !== "local"}
                        onClick={() => void handleVisualOCRSetup(true)}
                      >
                        {locale === "vi" ? "Cài OCR local" : "Install local OCR"}
                      </button>
                      {ocrHealthMessage && <span>{ocrHealthMessage}</span>}
                    </div>
                    <p className="worker-help">
                      {locale === "vi"
						? (ocrEngine === "colab"
							? "OCR chỉ đọc chữ/phụ đề hiển thị trong khung hình. KOVA tải video về trước rồi gửi video và vùng quét đến notebook OCR Colab riêng; máy local không cần Paddle/PaddleOCR."
							: "OCR local chỉ đọc chữ/phụ đề đã hiển thị trong khung hình và cần Python có OpenCV, PaddlePaddle, PaddleOCR. Vùng mặc định là dải phụ đề phía dưới: X 0.10, Y 0.70, Rộng 0.80, Cao 0.20.")
						: (ocrEngine === "colab"
							? "OCR reads visible captions only. KOVA downloads the video first, then sends the video and selected region to the separate Colab OCR notebook; local Paddle/PaddleOCR is not required."
							: "Local OCR reads only hardcoded text already visible in frames and needs Python with OpenCV, PaddlePaddle, and PaddleOCR. The default ROI is the lower subtitle band.")}
                    </p>
                  </>
                )}
				{sourceMethod === "speech_to_text_and_visual_ocr" && (
					<p className="worker-help">
						{locale === "vi"
							? `Kết hợp dùng STT làm mốc thời gian, sau đó OCR sửa các cue trùng thời lượng. OCR dùng vùng hiện tại X ${ocrRegionX}, Y ${ocrRegionY}, Rộng ${ocrRegionWidth}, Cao ${ocrRegionHeight}; chuyển sang lựa chọn OCR để điều chỉnh vùng nếu cần.`
							: `The combined mode uses STT as the timing backbone, then OCR corrects overlapping cues. OCR uses the current ROI X ${ocrRegionX}, Y ${ocrRegionY}, width ${ocrRegionWidth}, height ${ocrRegionHeight}; switch to OCR mode to adjust it if needed.`}
					</p>
				)}
              </div>
            )}
			{activeStage === "outputs" && (
			  <div className="worker-form render-options">
				<h2>{locale === "vi" ? "Video cuối: tự chèn phụ đề" : "Final video: automatic subtitles"}</h2>
				<p className="worker-help">
				  {locale === "vi"
					? "Khi bấm Bắt đầu bước này, KOVA tự burn SRT đã duyệt vào video cuối. Nếu video gốc có chữ/phụ đề cũ, bật che mờ để phụ đề mới được vẽ phía trên."
					: "When this step starts, KOVA automatically burns the approved SRT into the final video. Enable the mask when the source has hardcoded captions; the new subtitle is rendered above it."}
				</p>
				<section className="capcut-editable-export">
				  <h3>{locale === "vi" ? "Project CapCut có thể chỉnh sửa" : "Editable CapCut project"}</h3>
				  <p>
					{locale === "vi"
						? "Ngoài MP4 xem trước, KOVA có thể tạo project CapCut trực tiếp với video nguồn, audio lồng tiếng và từng track phụ đề là các lớp riêng để tiếp tục sửa trong CapCut."
						: "Alongside the preview MP4, KOVA can create a direct CapCut project with source video, dubbed audio, and separate editable subtitle tracks."}
				  </p>
				  <label className="ocr-gpu-toggle">
					<input
					  type="checkbox"
					  checked={capCutSettings.enabled}
					  onChange={(event) => setCapCutSettings((current) => ({ ...current, enabled: event.target.checked }))}
					/>
					{locale === "vi" ? "Tạo project CapCut chỉnh sửa được khi xuất" : "Create an editable CapCut project on export"}
				  </label>
				  <label>
					{locale === "vi" ? "Thư mục CapCut Drafts" : "CapCut Drafts folder"}
					<input
					  value={capCutSettings.draft_root || capCutSettings.detected_draft_root || ""}
					  placeholder={locale === "vi" ? "Chọn thư mục CapCut Drafts" : "Choose the CapCut Drafts folder"}
					  onChange={(event) => setCapCutSettings((current) => ({ ...current, draft_root: event.target.value }))}
					/>
				  </label>
				  <div className="source-picker-actions">
					<button className="secondary" disabled={busy} onClick={() => void handleChooseCapCutDraftRoot()}>
					  {locale === "vi" ? "Chọn thư mục CapCut Drafts" : "Choose CapCut Drafts folder"}
					</button>
					<button className="secondary" disabled={busy} onClick={() => void handleSaveCapCutDraftSettings()}>
					  {locale === "vi" ? "Lưu cấu hình CapCut" : "Save CapCut settings"}
					</button>
				  </div>
				  <p className="worker-help">
					{locale === "vi"
						? "Cần cài pycapcut một lần trong Python đã chọn: py -m pip install pycapcut. Nếu chưa bật project trực tiếp, KOVA vẫn lưu gói timeline cùng video, audio và hai file SRT riêng để không mất dữ liệu chỉnh sửa."
						: "Install pycapcut once in the selected Python: py -m pip install pycapcut. If direct project export is not enabled, KOVA still saves the timeline package plus separate video, audio, and both SRT files."}
				  </p>
				</section>
				<label className="ocr-gpu-toggle">
				  <input
					type="checkbox"
					checked={blurOriginalText}
					onChange={(event) => setBlurOriginalText(event.target.checked)}
				  />
				  {locale === "vi" ? "Che mờ vùng chữ/phụ đề cũ" : "Blur the old caption/text region"}
				</label>
				{blurOriginalText && (
				  <>
					<div className="ocr-region-grid">
					  <label>X<input type="number" min="0" max="1" step="0.01" value={blurRegionX} onChange={(event) => setBlurRegionX(event.target.value)} /></label>
					  <label>Y<input type="number" min="0" max="1" step="0.01" value={blurRegionY} onChange={(event) => setBlurRegionY(event.target.value)} /></label>
					  <label>{locale === "vi" ? "Rộng" : "Width"}<input type="number" min="0.01" max="1" step="0.01" value={blurRegionWidth} onChange={(event) => setBlurRegionWidth(event.target.value)} /></label>
					  <label>{locale === "vi" ? "Cao" : "Height"}<input type="number" min="0.01" max="1" step="0.01" value={blurRegionHeight} onChange={(event) => setBlurRegionHeight(event.target.value)} /></label>
					</div>
					<label>
					  {locale === "vi" ? "Độ mạnh che mờ (1–11)" : "Blur strength (1–11)"}
					  <input type="number" min="1" max="11" step="1" value={blurStrength} onChange={(event) => setBlurStrength(event.target.value)} />
					</label>
				  </>
				)}
			  </div>
			)}
			{activeStage === "source" && activeRun?.status === "running" && (
			  <p className="worker-help">
				{locale === "vi"
				  ? "KOVA đang tải riêng video và audio nguồn để bạn xem trước. Bước này không chạy STT, OCR hay dịch."
				  : "KOVA is downloading only the source video and audio for review. This step does not run STT, OCR, or translation."}
			  </p>
			)}
			{activeStage === "translation" && activeRun?.status === "running" && (
              <p className="worker-help">
				{sourceMethod === "speech_to_text_and_visual_ocr"
					? locale === "vi"
						? "KOVA đang tải video/audio, tạo transcript bằng STT rồi OCR vùng phụ đề để sửa các cue trùng thời lượng. Hai bản riêng và SRT kết hợp đều được lưu để kiểm tra."
						: "KOVA is downloading video/audio, creating a timed STT transcript, then OCRing the caption region to correct aligned cues. Both individual transcripts and the combined review SRT are saved."
					: sourceMethod === "visual_ocr"
                  ? locale === "vi"
                    ? "KOVA đang tải video/audio rồi quét vùng OCR để tạo SRT/script gốc có timestamp. Không chạy speech-to-text và không cần phụ đề YouTube."
                    : "KOVA is downloading the source, then scanning the selected OCR region to create a timestamped source SRT/script. It does not run speech-to-text or require YouTube captions."
                  : locale === "vi"
                    ? "KOVA đang tải video/audio, sau đó chạy speech-to-text để tạo SRT gốc có timestamp. Không cần phụ đề YouTube."
                    : "KOVA is downloading the video/audio, then running speech-to-text to create a timestamped source SRT. YouTube captions are not required."}
              </p>
            )}
            {persistentStage(activeStage) &&
              snapshot &&
              (activeRun || activeStage === "source") && (
                <label className="draft-editor">
                  {activeStage === "source"
                    ? sourceSRTAvailable
                      ? locale === "vi"
                        ? "SRT/script gốc — kiểm tra và sửa"
                        : "Source SRT/script — review and edit"
                      : locale === "vi"
                        ? "URL nguồn — sửa rồi chạy lại"
                        : "Source URL — edit and retry"
                    : activeStage === "translation"
                      ? locale === "vi"
                        ? "SRT tiếng Việt — kiểm tra và sửa"
                        : "Vietnamese SRT — review and edit"
                      : t(locale, "edit")}
                  <textarea
                    value={draft}
                    placeholder={
                      activeStage === "source"
                        ? sourceSRTAvailable
                          ? locale === "vi"
                            ? "Kiểm tra và sửa SRT/script gốc trước khi duyệt."
                            : "Review and edit the source SRT/script before approval."
                          : locale === "vi"
                            ? "Dán URL video nguồn trước khi chạy."
                            : "Paste the source video URL before starting."
                        : t(locale, "draftPlaceholder")
                    }
                    onChange={(event) => setDraft(event.target.value)}
                    disabled={
                      activeRun?.status === "running" ||
                      activeRun?.status === "approved"
                    }
                  />
                </label>
              )}
            {activeStage === "source" && !sourceSRTAvailable && (
              <>
              <div className="source-picker-actions">
                <button
                  className="secondary"
                  disabled={busy || activeRun?.status === "running" || activeRun?.status === "approved"}
                  onClick={() => void handleSelectSourceVideo("draft")}
                >
                  {locale === "vi" ? "Mở thư mục chọn video" : "Browse for a local video"}
                </button>
                <span>
                  {locale === "vi"
                    ? "Chọn MP4, MOV, MKV, WEBM hoặc AVI từ máy thay vì dán URL."
                    : "Choose an MP4, MOV, MKV, WEBM, or AVI file instead of pasting a URL."}
                </span>
              </div>
			  <label className="source-cookie-selector">
				{locale === "vi" ? "Phiên trình duyệt cho Douyin/TikTok" : "Browser session for Douyin/TikTok"}
				<select
				  value={sourceCookieBrowser}
				  disabled={busy || activeRun?.status === "running" || activeRun?.status === "approved"}
				  onChange={(event) => setSourceCookieBrowser(event.target.value as "auto" | "none" | "chrome" | "edge")}
				>
				  <option value="auto">{locale === "vi" ? "Tự động: phiên riêng KOVA (Edge rồi Chrome)" : "Auto: isolated KOVA session (Edge then Chrome)"}</option>
			  <option value="chrome">{locale === "vi" ? "Chrome (profile riêng KOVA, được lưu)" : "Chrome (saved isolated KOVA profile)"}</option>
			  <option value="edge">{locale === "vi" ? "Microsoft Edge (profile riêng KOVA, được lưu)" : "Microsoft Edge (saved isolated KOVA profile)"}</option>
				  <option value="none">{locale === "vi" ? "Tắt phiên trình duyệt KOVA" : "Disable the KOVA browser session"}</option>
				</select>
				<small>
				  {locale === "vi"
					? "Douyin chạy trong profile riêng của KOVA để trang web tự tạo chữ ký hợp lệ. Profile được giữ lại, tách biệt hoàn toàn với trình duyệt cá nhân."
					: "Douyin runs inside KOVA's isolated profile so the site creates a valid signature. The saved profile remains completely separate from your personal browser."}
				</small>
			  </label>
              <div className="source-picker-actions">
                <button
                  className="secondary"
                  disabled={busy || activeRun?.status === "running" || activeRun?.status === "approved" || sourceCookieBrowser === "none"}
                  onClick={() => void handleOpenShortVideoSession(draft)}
                >
                  {locale === "vi" ? "Thiết lập phiên Douyin/TikTok" : "Set up Douyin/TikTok session"}
                </button>
                <span>
                  {locale === "vi"
                    ? "Nếu có đăng nhập/CAPTCHA, hoàn tất và phát video một lần, sau đó đóng cửa sổ rồi chạy lại bước tải."
                    : "If sign-in/CAPTCHA appears, complete it and play the video once, then close the window and restart the download step."}
                </span>
              </div>
			  </>
            )}
            {stageMessage && <p className="stage-message">{stageMessage}</p>}
            {message && <p className="error-message">{message}</p>}
          </section>
          <footer className="stage-actions">
            <button
              className="secondary"
              disabled={busy || !canSaveDraft}
              onClick={() => void handleSaveDraft()}
            >
              {t(locale, "saveDraft")}
            </button>
            <button
              className="primary"
              disabled={busy || !canStart}
              onClick={() => void handleStartStage()}
            >
              {t(locale, "start")}
            </button>
            <button
              className="secondary"
              disabled={busy || !snapshot?.project.workflow_task_id}
              onClick={() => void handleRefreshWorkflow()}
            >
              {t(locale, "refreshWorkflow")}
            </button>
            <button
              className="secondary"
              disabled={
                busy ||
                Boolean(snapshot?.project.workflow_task_id) ||
                !activeRun ||
                activeRun.status !== "running"
              }
              onClick={() => void handleMarkForReview()}
            >
              {t(locale, "sendForReview")}
            </button>
            <button
              className="success"
              disabled={busy || !canApprove}
              onClick={() => void handleApprove()}
            >
              {t(locale, "approve")}
            </button>
          </footer>
            </>
          )}
        </section>

        <aside className="right-pane">
          <div className="right-tabs">
            <button className="selected">✦ {t(locale, "style")}</button>
            <button>◎ {t(locale, "review")}</button>
            <button>⌁ {t(locale, "ocr")}</button>
            <button>◷ {t(locale, "history")}</button>
          </div>
          <section className="inspector-card">
            <h2>{t(locale, "review")}</h2>
            <p>{stageMessage || t(locale, "noProject")}</p>
          </section>
          <section className="inspector-card">
            <h2>
              {locale === "vi" ? "Artifact từ worker" : "Worker artifacts"}
            </h2>
            {workflowArtifacts.length ? (
              <ul className="artifact-list">
                {workflowArtifacts.map((artifact) => (
                  <li key={`${artifact.kind}-${artifact.download_url}`}>
                    <strong>{artifact.label || artifact.kind}</strong>
                    <a
                      href={workflowArtifactURL(
                        data.legacy_api_base_url,
                        artifact.download_url,
                      )}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {artifact.name || artifact.download_url}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p>
                {locale === "vi"
                  ? "Worker chưa trả artifact nào."
                  : "The worker has not returned an artifact yet."}
              </p>
            )}
          </section>
          <section className="inspector-card">
            <h2>{t(locale, "artifacts")}</h2>
            {Array.isArray(snapshot?.artifacts) && snapshot.artifacts.length ? (
              <ul className="artifact-list">
                {snapshot.artifacts
                  .slice()
                  .reverse()
                  .map((artifact) => (
                    <li key={artifact.id}>
                      <strong>{artifact.kind}</strong>
                      <span>{artifact.path}</span>
                    </li>
                  ))}
              </ul>
            ) : (
              <p>{t(locale, "noArtifacts")}</p>
            )}
          </section>
        </aside>
      </section>
    </main>
  );
}

function asMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

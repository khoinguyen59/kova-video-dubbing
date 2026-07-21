import type { Locale, StageId } from './types'

const dictionary = {
  vi: {
    appTitle: 'KOVA · Studio bản địa hóa video',
    project: 'Dự án', language: 'Ngôn ngữ', settings: 'Cài đặt', history: 'Lịch sử', style: 'Phong cách', review: 'Kiểm tra', ocr: 'OCR', ready: 'Sẵn sàng',
    start: 'Bắt đầu bước này', approve: 'Duyệt đầu ra', edit: 'Kiểm tra và sửa', saveDraft: 'Lưu bản kiểm tra', sendForReview: 'Đưa đi kiểm tra', refreshWorkflow: 'Cập nhật đầu ra',
    newProject: 'Dự án mới', newProjectName: 'Tên dự án', createProject: 'Tạo dự án', selectProject: 'Chọn dự án', noProject: 'Tạo hoặc chọn một dự án để bắt đầu.',
    openColab: 'Mở notebook trên Google Colab', checkConnection: 'Kiểm tra kết nối', loadProfiles: 'Tải danh sách giọng', colabUrl: 'URL Colab Worker', colabToken: 'Token Colab',
    ttsProvider: 'Bộ máy TTS', fixedVoice: 'Giọng clone cố định', noProfile: 'Chưa chọn profile', profileHelp: 'Chọn đúng một profile đã có sự đồng ý để toàn bộ video giữ nguyên một giọng.',
    sourceHint: 'Nhập URL hoặc chọn video cục bộ. Lưu bản kiểm tra, sau đó tự đưa bước này vào trạng thái cần duyệt.',
    translationHint: 'Dán hoặc chỉnh SRT/script tiếng Việt ở đây. KOVA chỉ cho phép tiếp tục sau khi bạn duyệt bản dịch.',
    dubbingHint: 'Chọn một profile/version cố định hoặc một preset TTS trong danh sách xổ xuống. Không dùng nhiều giọng trong cùng một lần chạy.',
    renderHint: 'Kiểm tra bản xem trước, phụ đề và nhịp âm thanh trước khi xuất video hoàn chỉnh.',
    outputsHint: 'Các artifact đã duyệt được liệt kê ở đây để mở, tải hoặc xuất sang CapCut.',
    draftPlaceholder: 'Dán URL, script, phụ đề hoặc ghi chú kiểm tra của bước này…',
    artifacts: 'Đầu ra đã lưu', noArtifacts: 'Chưa có đầu ra nào được lưu.',
    stageRunning: 'Bước đang chạy. Khi worker hoàn tất, bấm Cập nhật đầu ra để lấy artifact rồi kiểm tra.', stageReview: 'Bản kiểm tra đã sẵn sàng. Bạn có thể duyệt hoặc quay lại sửa.', stageApproved: 'Đầu ra đã được duyệt. Bước kế tiếp hiện có thể bắt đầu.',
    connectionSuccess: 'Kết nối Voice Studio thành công.', connectionFailed: 'Không thể kết nối Voice Studio.', profilesLoaded: 'Đã tải danh sách profile.', actionFailed: 'Thao tác không thành công:',
  },
  en: {
    appTitle: 'KOVA · Video Localization Studio',
    project: 'Project', language: 'Language', settings: 'Settings', history: 'History', style: 'Style', review: 'Review', ocr: 'OCR', ready: 'Ready',
    start: 'Start this stage', approve: 'Approve output', edit: 'Review and edit', saveDraft: 'Save review draft', sendForReview: 'Send to review', refreshWorkflow: 'Refresh outputs',
    newProject: 'New project', newProjectName: 'Project name', createProject: 'Create project', selectProject: 'Select project', noProject: 'Create or select a project to begin.',
    openColab: 'Open notebook in Google Colab', checkConnection: 'Check connection', loadProfiles: 'Load voice profiles', colabUrl: 'Colab Worker URL', colabToken: 'Colab token',
    ttsProvider: 'TTS engine', fixedVoice: 'Fixed clone voice', noProfile: 'No profile selected', profileHelp: 'Choose exactly one consented profile so the whole video retains one voice.',
    sourceHint: 'Paste a URL or choose a local video. Save the review draft, then explicitly send this stage for approval.',
    translationHint: 'Paste or edit the Vietnamese SRT/script here. KOVA blocks the next stage until you approve the translation.',
    dubbingHint: 'Choose one fixed profile/version or a preset TTS option from the dropdown. Do not mix voices within one run.',
    renderHint: 'Review the preview, subtitles, and audio timing before rendering the complete video.',
    outputsHint: 'Approved artifacts appear here to open, download, or export to CapCut.',
    draftPlaceholder: 'Paste the URL, script, subtitles, or review notes for this stage…',
    artifacts: 'Saved outputs', noArtifacts: 'No output has been saved yet.',
    stageRunning: 'The stage is running. When the worker completes, use Refresh outputs to retrieve artifacts and review them.', stageReview: 'The review draft is ready. You may approve it or return to editing.', stageApproved: 'The output is approved. The next stage can now be started.',
    connectionSuccess: 'Voice Studio connection succeeded.', connectionFailed: 'Voice Studio connection failed.', profilesLoaded: 'Voice profiles loaded.', actionFailed: 'Action failed:',
  },
} as const

export type TranslationKey = keyof (typeof dictionary)['vi']

export function t(locale: Locale, key: TranslationKey): string { return dictionary[locale][key] }

export function stageTitle(locale: Locale, stage: { id: StageId; title_vi: string; title_en: string }): string {
  return locale === 'vi' ? stage.title_vi : stage.title_en
}

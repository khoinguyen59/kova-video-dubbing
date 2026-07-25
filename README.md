# Kova

Kova là desktop app dịch video, tạo phụ đề và lồng tiếng cố định. Giao diện chính dùng tiếng Việt/English; giao diện trình duyệt chỉ là test console.

Kova is a desktop-first video localization app for transcription, translation, fixed-voice dubbing, and final MP4 rendering. The browser surface is provided for API testing only.

## Luồng chính / Main workflow

Năm bước nằm ở thanh điều hướng trái của desktop app:

1. `Nguồn video / Video source` — file trên máy hoặc URL công khai; chọn
   **Speech-to-text từ audio** hoặc **OCR phụ đề hiển thị trong video** để tạo
   SRT/script gốc, rồi kiểm tra và duyệt trước khi dịch.
2. `Dịch và phụ đề / Translation` — chọn ngôn ngữ, chế độ song ngữ và tên riêng.
3. `Giọng lồng tiếng cố định / Fixed voice` — một engine/profile hoặc một audio clone cho toàn video.
4. `Xuất hình và tinh chỉnh / Video output` — ngang, dọc, cả hai hoặc chỉ SRT/audio.
5. `Chạy và nhận output / Run` — tiến độ và các link SRT, WAV/MP3, MP4.

## Model và API của Kova

### Dịch phụ đề

- Mặc định: **KOVA API Gateway** OpenAI-compatible tại `http://3.27.172.90/v1`.
- Dropdown dịch chỉ cho chọn sáu model free đã xác minh: `oc/big-pickle`,
  `oc/deepseek-v4-flash-free`, `oc/mimo-v2.5-free`, `oc/hy3-free`,
  `oc/nemotron-3-ultra-free`, `oc/north-mini-code-free`.
- Model mặc định: `oc/deepseek-v4-flash-free`. KOVA không cài Ollama và không
  tải model LLM về máy.
- API key đọc theo thứ tự từ phiên hiện tại, `config/config.toml` cục bộ bị Git
  bỏ qua, rồi biến môi trường `KOVA_API_GATEWAY_API_KEY`. Key không được trả
  qua API cấu hình, ghi vào project, notebook, hay GitHub.

### Lồng tiếng

- Mặc định: **KOVA Voice Studio**, dịch vụ clone giọng độc lập dùng core
  `k2-fsa/OmniVoice` qua worker GPU Google Colab (URL HTTPS tunnel).
- Một audio tham chiếu được dùng cho toàn job để giữ giọng cố định.
- Google TTS Vietnamese/English và Microsoft Edge TTS Vietnamese Hoài My/Nam
  Minh là lựa chọn dạng xổ xuống qua cùng KOVA API Gateway. Chúng là giọng
  preset, không phải clone giọng.
- Notebook Colab: `voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb` hoặc
  nút `Mở notebook Google Colab` trong app. Notebook chỉ mở khi người dùng bấm
  nút; sau khi chọn GPU và Run all, nó in URL HTTPS cùng token tạm thời để dán
  vào KOVA Voice Studio/KOVA Desktop.

### CapCut Auto-Builder, OCR và style phụ đề

- Tab `06 · CapCut Auto-Builder & OCR` có bố cục desktop ba cột: nguồn/config ở trái, preview kéo logo/vẽ ROI ở giữa, style/OCR/review ở phải.
- Kova luôn tạo `kova-capcut-draft-spec.json` để kiểm tra trước. File này lưu timeline ảnh/video, voiceover, BGM ngẫu nhiên + loop/ducking, motion, transition, logo, hai track subtitle độc lập và cấu hình style.
- Tab **05 · Chạy và nhận output** có tùy chọn xuất **project CapCut chỉnh sửa được**. Chọn thư mục `CapCut Drafts`, bật xuất project và cài `pycapcut` vào Python đã chọn. KOVA tạo draft thật với video nguồn, audio lồng tiếng, phụ đề gốc/bản dịch và vùng che mờ là các track/lớp riêng; MP4 chỉ là bản xem trước.
- Tab **01 · Tải video** chỉ tải video/audio để xem trước. Tab **02 · Tạo script, dịch và phụ đề** mới chạy Speech-to-Text và/hoặc Visual OCR, tạo SRT/script để kiểm tra trước khi dịch. OCR hỗ trợ Colab GPU riêng hoặc môi trường PaddleOCR local do người dùng chọn.
- Live preview cho từng track phụ đề có màu, viền, nền alpha, bóng, căn lề, vị trí dọc và preset. Danh sách font lấy từ font families đã cài trên Windows; tên font được lưu vào Kova draft để kiểm tra/chỉnh tiếp trong CapCut nếu backend ngoài không map được font hệ thống đó.

Xem thêm: [`docs/KOVA_DOCUMENTATION.md`](docs/KOVA_DOCUMENTATION.md).

### Kova API v1

| Method | Route | Mục đích / Purpose |
|---|---|---|
| `GET` | `/api/v1/status` | Trạng thái và capability của Kova |
| `POST` | `/api/v1/files` | Upload file nguồn |
| `POST` | `/api/v1/jobs/subtitle` | Tạo job dịch/lồng tiếng |
| `GET` | `/api/v1/jobs/subtitle?taskId=...` | Theo dõi job và nhận output |
| `GET` | `/api/v1/config` | Đọc cấu hình, không trả secret |
| `PUT` | `/api/v1/config` | Cập nhật cấu hình |

Các route `/api/capability/...` cũ chỉ còn alias tương thích và không được desktop app mới sử dụng.

## Chạy desktop trên Windows

### KOVA Desktop (Wails + React, ứng dụng chính)

Luôn đóng gói ứng dụng chính bằng script Wails của KOVA. Không chạy `go build .`:
lệnh đó không tạo các binding và tài nguyên WebView cần cho giao diện React,
nên có thể mở một cửa sổ trống hoặc báo lỗi build tag.

```powershell
.\scripts\build-wails.ps1 -WailsPath "C:\duong-dan\toi\wails.exe"
```

Script đọc `VERSION` theo dạng `A.B.C.D`: `D` tăng từ `0` đến `9`; sau `.9`,
`C` tăng một đơn vị và `D` trở về `0` (ví dụ `1.0.1.9 → 1.0.2.0`). Script giữ
duy nhất file `build\\KOVA-Desktop-<version>.exe`. Wails sẽ tự tạo binding
trong `frontend/wailsjs` và tự build frontend trước khi nhúng vào `.exe`; hai
thư mục đó là artefact build, không cần commit.

### Giao diện tương thích Fyne

Yêu cầu:

- Windows x64.
- `ffmpeg`, `ffprobe`, `yt-dlp` trong `PATH` hoặc thư mục `bin`.
- Với FasterWhisper local: model và binary tương ứng trong `models`/`bin`.
- Nếu muốn Kova tự tải archive còn thiếu: đặt `KOVA_DEPENDENCY_BASE_URL` tới mirror chứa đúng các asset Kova nêu trong log.
- Với URL YouTube bị giới hạn: có thể cần cookies của tài khoản có quyền xem.
- Với dịch hoặc Google/Edge TTS Gateway: `KOVA_API_GATEWAY_API_KEY` (hoặc key
  cục bộ trong `config/config.toml`, không commit).
- Với OmniVoice: URL HTTPS tunnel `https://...trycloudflare.com` và token tạm
  thời do notebook Colab GPU in ra. KOVA Desktop không chạy clone giọng trên
  máy desktop; profile/reference audio thuộc KOVA Voice Studio độc lập.

Build:

```powershell
go build -trimpath -ldflags "-s -w -H=windowsgui" -o build/Kova-Desktop-Windows-x64.exe ./cmd/desktop
```

Chạy:

```powershell
.\build\Kova-Desktop-Windows-x64.exe
```

## CLI

```powershell
go build -o build/kova-cli.exe ./cmd/cli
.\build\kova-cli.exe --help
```

CLI ghi manifest vào `kova_manifest.json`.

## Bảo mật / Security

- Không commit API key, cookie hoặc token.
- `GET /api/v1/config` không trả secret đã lưu.
- Khi cập nhật config với key rỗng, Kova giữ secret hiện có thay vì xóa.
- Chỉ dùng audio clone và nội dung mà bạn có quyền xử lý.

## Kiểm thử / Tests

```powershell
go test -tags ci -timeout 300s ./... -count=1
```

Test console: khởi động server rồi mở `http://127.0.0.1:8888/static`.

## Nguồn gốc và giấy phép / Attribution

Kova là một fork đã được tái cấu trúc mạnh. Thông tin attribution và giấy phép của phần mã nguồn kế thừa được giữ trong `LICENSE` và lịch sử Git; tên sản phẩm, module, desktop UI, API v1 và tài liệu vận hành hiện thuộc Kova.

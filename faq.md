# Kova FAQ

## Kova có cần cài Ollama không?

Không. Cấu hình mặc định gọi Ollama Cloud trực tiếp với model `deepseek-v4-flash:cloud`. Chỉ cần đặt `OLLAMA_API_KEY`.

## Tại sao URL YouTube không tải được?

Video riêng tư, giới hạn tuổi/khu vực hoặc bị anti-bot có thể cần cookie của tài khoản có quyền xem. Cập nhật `yt-dlp`, kiểm tra proxy và chỉ dùng cookie của chính bạn.

## Làm sao chạy OmniVoice bằng GPU Colab?

Mở notebook từ nút trong app, chọn GPU, `Run all`, chờ cell cuối in URL `https://...trycloudflare.com`, dán URL đó vào `OmniVoice Worker URL`, rồi nhấn kiểm tra kết nối.

## Làm sao giữ một giọng từ đầu đến cuối?

Chọn `KOVA Voice Studio (clone giọng)`, bật lồng tiếng và chọn một audio tham chiếu rõ tiếng. Kova dùng cùng reference/profile cho toàn bộ job.

## Google TTS nằm ở đâu?

Trong tab `03 · Giọng lồng tiếng cố định`, mở danh sách `Công cụ TTS` và chọn `Google TTS qua API Gateway`. Đây là dropdown, không phải ô nhập tay.

## Output hoàn chỉnh ở đâu?

Tab `05 · Chạy và nhận output` hiển thị link SRT, audio và MP4. MP4 chỉ được tạo khi tab `04` bật xuất video.

## Cần những dependency nào?

`ffmpeg`, `ffprobe`, `yt-dlp`; FasterWhisper cần binary/model local. LLM Ollama Cloud không tải model local. OmniVoice clone chỉ dùng worker GPU Google Colab qua URL HTTPS; Kova không clone trên desktop.

## API key có được trả về trình duyệt không?

Không. `GET /api/v1/config` che các secret. Khi `PUT` một key rỗng, Kova giữ key hiện có.

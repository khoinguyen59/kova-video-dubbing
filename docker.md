# Kova Docker

Build server binary:

```bash
CGO_ENABLED=0 go build -o Kova ./cmd/server
docker build -t kova:local .
```

Run:

```bash
docker run --rm -p 8888:8888 \
  -e OLLAMA_API_KEY \
  -v /path/to/config.toml:/app/config/config.toml \
  -v /path/to/tasks:/app/tasks \
  -v /path/to/models:/app/models \
  -v /path/to/bin:/app/bin \
  kova:local
```

Trong container, đặt `server.host = "0.0.0.0"`. OmniVoice chạy ngoài container phải dùng URL worker mà container truy cập được; với Google Colab, dùng URL tunnel do notebook in ra.

Set `server.host = "0.0.0.0"` inside the container. An external OmniVoice worker must be reachable from the container; for Colab, use the tunnel URL printed by the notebook.

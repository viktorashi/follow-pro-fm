FROM fedirz/faster-whisper-server:latest-cpu AS downloader

ENV WHISPER_MODEL=tiny
# Pre-download the model into /root/.cache/huggingface
RUN uv run python -c "from faster_whisper import download_model; download_model('tiny')"

FROM fedirz/faster-whisper-server:latest-cpu
# Copy only the downloaded model cache, leaving behind any uv/pip download cache
COPY --from=downloader /root/.cache/huggingface /root/.cache/huggingface

ENV WHISPER_MODEL=tiny

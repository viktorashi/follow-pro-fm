FROM fedirz/faster-whisper-server:latest-cpu AS downloader

ENV WHISPER_MODEL=base
# Pre-download the model into /root/.cache/huggingface
RUN uv run python -c "from faster_whisper import download_model; download_model('base')"

FROM fedirz/faster-whisper-server:latest-cpu
# Copy only the downloaded model cache, leaving behind any uv/pip download cache
COPY --from=downloader /root/.cache/huggingface /root/.cache/huggingface

ENV UVICORN_HOST="::" \
    ENABLE_UI="false" \
    WHISPER_MODEL="base" \
    WHISPER__MODEL="base" \
    WHISPER__COMPUTE_TYPE="int8" \
    PRELOAD_MODELS='["base"]' \
    DEFAULT_LANGUAGE="ro" \
    OMP_NUM_THREADS="4"

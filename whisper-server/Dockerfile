ARG WHISPER_IMAGE=fedirz/faster-whisper-server@sha256:760e5e43d427dc6cfbbc4731934b908b7de9c7e6d5309c6a1f0c8c923a5b6030
FROM ${WHISPER_IMAGE} AS downloader

ENV WHISPER_MODEL=base
# Pre-download the model into /root/.cache/huggingface
RUN uv run python -c "import os; from faster_whisper import download_model; download_model(os.environ['WHISPER_MODEL'])"

FROM ${WHISPER_IMAGE}
# Copy only the downloaded model cache, leaving behind any uv/pip download cache
COPY --from=downloader /root/.cache/huggingface /root/.cache/huggingface

ENV UVICORN_HOST="0.0.0.0" \
    ENABLE_UI="false" \
    WHISPER_MODEL="base" \
    WHISPER__MODEL="base" \
    WHISPER__COMPUTE_TYPE="int8" \
    DEFAULT_LANGUAGE="ro" \
    OMP_NUM_THREADS="4"

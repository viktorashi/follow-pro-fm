ARG SPEACHES_IMAGE=ghcr.io/speaches-ai/speaches:0.8.3-cpu@sha256:21e3df06d842fb7802ab470dd77c25f0e8c0d22950e8d8c6ae886e851af53ef8
FROM ${SPEACHES_IMAGE} AS downloader
ARG FASTER_WHISPER_VERSION=1.2.1
RUN uv pip install --python .venv/bin/python "faster-whisper==${FASTER_WHISPER_VERSION}" && \
    python -c "from speaches.executors.whisper.utils import model_registry; model_registry.download_model_files('Systran/faster-whisper-base')"

FROM ${SPEACHES_IMAGE}
ARG FASTER_WHISPER_VERSION=1.2.1
RUN uv pip install --python .venv/bin/python "faster-whisper==${FASTER_WHISPER_VERSION}" && \
    sed -i 's/turn_detection: TurnDetection | NotGiven/turn_detection: TurnDetection | None | NotGiven/' src/speaches/types/realtime.py

# Copy only the downloaded model cache, leaving behind any uv/pip download cache
COPY --from=downloader /home/ubuntu/.cache/huggingface /home/ubuntu/.cache/huggingface

ENV UVICORN_HOST="0.0.0.0" \
    LOOPBACK_HOST_URL="http://127.0.0.1:8000" \
    ENABLE_UI="false" \
    WHISPER__INFERENCE_DEVICE="cpu" \
    WHISPER__COMPUTE_TYPE="int8" \
    WHISPER__CPU_THREADS="1" \
    OMP_NUM_THREADS="1"

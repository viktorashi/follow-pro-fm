FROM fedirz/faster-whisper-server:latest-cpu

ENV WHISPER_MODEL=base

# Pre-download the model at build time to bake it into the image
RUN uv run python -c "from faster_whisper import download_model; download_model('base')"

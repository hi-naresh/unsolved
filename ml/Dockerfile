# syntax=docker/dockerfile:1
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    HF_HOME=/app/hf

WORKDIR /app

# CPU-only torch first, so sentence-transformers doesn't pull the CUDA build.
RUN pip install --index-url https://download.pytorch.org/whl/cpu torch
COPY requirements.txt .
RUN pip install -r requirements.txt

# Bake the model into the image: no download at boot, no network needed.
RUN python -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('sentence-transformers/all-MiniLM-L6-v2')"
ENV HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1

COPY app ./app

RUN useradd --system --uid 10001 ml && chown -R ml /app
USER ml

EXPOSE 8000
# "::" so the service is reachable on Fly's IPv6 private network (6PN).
CMD ["uvicorn", "app.main:app", "--host", "::", "--port", "8000", "--workers", "1"]

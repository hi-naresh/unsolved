"""Unsolved ML service (phase 5).

Stateless: text in, vectors or tags out. It never touches the database; the Go
server stores whatever it returns.

    POST /embed   {"texts": [...]}  -> {"vectors": [[384 floats], ...]}
    POST /tag     {"text": "..."}   -> {"tags": [...]}
    GET  /healthz                   -> {"status": "ok", "model": ...}

Embeddings come from sentence-transformers/all-MiniLM-L6-v2 with
normalize_embeddings=True, so cosine similarity is a plain dot product (and
pgvector's `<=>` cosine distance is 1 - dot).

The model is loaded once at startup. Tests inject a fake encoder through
create_app(encoder=...) so they never need torch or the model weights.
"""

from __future__ import annotations

import os
from contextlib import asynccontextmanager
from typing import Annotated, Protocol, Sequence

import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field, StringConstraints

MODEL_NAME = "sentence-transformers/all-MiniLM-L6-v2"
DIM = 384

MAX_TEXTS = 64
MAX_CHARS = 8000

# Tagging: cosine similarity of the text against each label's description.
TAG_THRESHOLD = float(os.environ.get("TAG_THRESHOLD", "0.25"))
TAG_TOP_K = 3

# Domain labels are the seeded domain slugs (migrations/0002_domains.sql).
DOMAIN_LABELS: dict[str, str] = {
    "manufacturing": "manufacturing, factory production lines and machinery",
    "healthcare": "healthcare, clinics, hospitals, patients and care",
    "logistics": "logistics, shipping, warehouses, freight and deliveries",
    "retail": "retail, shops, stores, customers and point of sale",
    "professional_services": "professional services, accounting, law firms, consultancy and agencies",
    "hospitality": "hospitality, restaurants, hotels, bars and catering",
    "construction": "construction, building sites, contractors and trades",
    "education": "education, schools, teachers, students and universities",
    "nonprofit": "nonprofit, charities, volunteers, donors and fundraising",
    "other": "other general business operations",
}

# Process themes: what kind of work the problem is about.
THEME_LABELS: dict[str, str] = {
    "scheduling": "scheduling shifts, rotas, appointments and bookings",
    "invoicing": "invoicing, billing, payments and chasing money owed",
    "inventory": "inventory, stock levels, stocktakes and reordering",
    "reporting": "reporting, spreadsheets, dashboards and monthly reports",
    "compliance": "compliance, regulations, audits, certificates and paperwork",
    "handover": "handover between shifts, teams or people",
    "customer communication": "customer communication, emails, phone calls and follow-ups",
    "data entry": "data entry, retyping information between systems and forms",
}

ALL_LABELS: dict[str, str] = {**DOMAIN_LABELS, **THEME_LABELS}


class Encoder(Protocol):
    """Anything that turns texts into L2-normalised vectors of shape (n, DIM)."""

    def encode(self, texts: Sequence[str]) -> np.ndarray: ...


class SentenceTransformerEncoder:
    """The real encoder. Imports sentence_transformers lazily so the module
    (and the tests) load without torch installed."""

    def __init__(self, model_name: str = MODEL_NAME) -> None:
        from sentence_transformers import SentenceTransformer  # heavy import

        self._model = SentenceTransformer(model_name, device="cpu")

    def encode(self, texts: Sequence[str]) -> np.ndarray:
        return self._model.encode(
            list(texts),
            batch_size=32,
            normalize_embeddings=True,
            convert_to_numpy=True,
            show_progress_bar=False,
        )


Text = Annotated[str, StringConstraints(max_length=MAX_CHARS)]


class EmbedRequest(BaseModel):
    texts: Annotated[list[Text], Field(min_length=1, max_length=MAX_TEXTS)]


class EmbedResponse(BaseModel):
    vectors: list[list[float]]


class TagRequest(BaseModel):
    text: Annotated[str, StringConstraints(min_length=1, max_length=MAX_CHARS)]


class TagResponse(BaseModel):
    tags: list[str]


def _normalise(v: np.ndarray) -> np.ndarray:
    v = np.asarray(v, dtype=np.float32)
    norms = np.linalg.norm(v, axis=1, keepdims=True)
    norms[norms == 0] = 1.0
    return v / norms


def create_app(encoder: Encoder | None = None) -> FastAPI:
    """Build the app. With encoder=None the real model loads at startup."""

    state: dict[str, object] = {"encoder": encoder, "labels": None}

    def get_encoder() -> Encoder:
        enc = state["encoder"]
        if enc is None:
            raise HTTPException(status_code=503, detail="model not loaded")
        return enc  # type: ignore[return-value]

    def label_matrix() -> tuple[list[str], np.ndarray]:
        # Label embeddings are fixed; compute them once per process.
        if state["labels"] is None:
            names = list(ALL_LABELS)
            vecs = _normalise(get_encoder().encode([ALL_LABELS[n] for n in names]))
            state["labels"] = (names, vecs)
        return state["labels"]  # type: ignore[return-value]

    @asynccontextmanager
    async def lifespan(_: FastAPI):
        if state["encoder"] is None:
            state["encoder"] = SentenceTransformerEncoder()
        yield

    app = FastAPI(title="unsolved-ml", lifespan=lifespan, docs_url=None, redoc_url=None)

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        get_encoder()
        return {"status": "ok", "model": MODEL_NAME}

    # Plain `def` endpoints: FastAPI runs them in its threadpool, so the
    # CPU-bound encode never blocks the event loop.
    @app.post("/embed", response_model=EmbedResponse)
    def embed(req: EmbedRequest) -> EmbedResponse:
        vecs = _normalise(get_encoder().encode(req.texts))
        if vecs.shape != (len(req.texts), DIM):
            raise HTTPException(status_code=500, detail=f"encoder returned shape {vecs.shape}")
        return EmbedResponse(vectors=vecs.tolist())

    @app.post("/tag", response_model=TagResponse)
    def tag(req: TagRequest) -> TagResponse:
        names, labels = label_matrix()
        v = _normalise(get_encoder().encode([req.text]))[0]
        sims = labels @ v
        order = np.argsort(-sims)
        tags = [names[i] for i in order if sims[i] >= TAG_THRESHOLD][:TAG_TOP_K]
        return TagResponse(tags=tags)

    return app


app = create_app()

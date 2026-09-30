"""API tests with a deterministic fake encoder: no torch, no model download."""

from __future__ import annotations

import hashlib
import re
from typing import Sequence

import numpy as np
import pytest
from fastapi.testclient import TestClient

from app.main import DIM, DOMAIN_LABELS, MAX_CHARS, MAX_TEXTS, THEME_LABELS, create_app


class FakeEncoder:
    """Bag of hashed words: texts sharing words get similar vectors."""

    def __init__(self) -> None:
        self.calls = 0

    def encode(self, texts: Sequence[str]) -> np.ndarray:
        self.calls += 1
        out = np.zeros((len(texts), DIM), dtype=np.float32)
        for i, t in enumerate(texts):
            for w in re.findall(r"[a-z]+", t.lower()):
                h = int.from_bytes(hashlib.sha256(w.encode()).digest()[:4], "big")
                out[i, h % DIM] += 1.0
            if not out[i].any():
                out[i, 0] = 1.0
        return out / np.linalg.norm(out, axis=1, keepdims=True)


@pytest.fixture()
def enc() -> FakeEncoder:
    return FakeEncoder()


@pytest.fixture()
def client(enc: FakeEncoder):
    with TestClient(create_app(encoder=enc)) as c:
        yield c


def test_healthz(client: TestClient) -> None:
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_embed_shape_and_normalised(client: TestClient) -> None:
    r = client.post("/embed", json={"texts": ["weekly rota in a spreadsheet", "chasing unpaid invoices"]})
    assert r.status_code == 200
    vecs = np.array(r.json()["vectors"])
    assert vecs.shape == (2, DIM)
    assert np.allclose(np.linalg.norm(vecs, axis=1), 1.0, atol=1e-5)


def test_embed_is_deterministic_and_similar_texts_are_close(client: TestClient) -> None:
    texts = [
        "we copy the delivery notes into the stock spreadsheet by hand",
        "we copy the delivery notes into the stock spreadsheet by hand every day",
        "patients book appointments by phone",
    ]
    a = np.array(client.post("/embed", json={"texts": texts}).json()["vectors"])
    b = np.array(client.post("/embed", json={"texts": texts}).json()["vectors"])
    assert np.allclose(a, b)
    assert a[0] @ a[1] > 0.85
    assert a[0] @ a[2] < 0.5


@pytest.mark.parametrize(
    "body",
    [
        {"texts": []},
        {"texts": ["x"] * (MAX_TEXTS + 1)},
        {"texts": ["x" * (MAX_CHARS + 1)]},
        {"texts": "not a list"},
        {},
    ],
)
def test_embed_rejects_bad_batches(client: TestClient, body: dict) -> None:
    assert client.post("/embed", json=body).status_code == 422


def test_embed_accepts_limits(client: TestClient) -> None:
    r = client.post("/embed", json={"texts": ["x" * MAX_CHARS] * MAX_TEXTS})
    assert r.status_code == 200
    assert len(r.json()["vectors"]) == MAX_TEXTS


def test_tag_returns_known_labels(client: TestClient) -> None:
    r = client.post("/tag", json={"text": "invoicing billing payments and chasing money owed by clients"})
    assert r.status_code == 200
    tags = r.json()["tags"]
    assert tags and tags[0] == "invoicing"
    assert len(tags) <= 3
    known = set(DOMAIN_LABELS) | set(THEME_LABELS)
    assert all(t in known for t in tags)


def test_tag_below_threshold_is_empty(client: TestClient) -> None:
    r = client.post("/tag", json={"text": "zzz qqq"})
    assert r.status_code == 200
    assert r.json()["tags"] == []


def test_tag_label_embeddings_are_cached(client: TestClient, enc: FakeEncoder) -> None:
    client.post("/tag", json={"text": "stock levels"})
    first = enc.calls
    client.post("/tag", json={"text": "stock levels again"})
    assert enc.calls == first + 1  # only the text itself, not the labels


@pytest.mark.parametrize("body", [{"text": ""}, {"text": "x" * (MAX_CHARS + 1)}, {}])
def test_tag_rejects_bad_input(client: TestClient, body: dict) -> None:
    assert client.post("/tag", json=body).status_code == 422

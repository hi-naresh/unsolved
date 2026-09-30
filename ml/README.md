# unsolved-ml

Phase 5 ML service. Stateless: text in, vectors or tags out. It never writes
anything; the Go server stores the results.

| Method | Path | Body | Returns |
| --- | --- | --- | --- |
| POST | `/embed` | `{"texts": ["..."]}` (1–64 texts, each ≤ 8000 chars) | `{"vectors": [[384 floats], ...]}` |
| POST | `/tag` | `{"text": "..."}` (≤ 8000 chars) | `{"tags": ["logistics", "data entry"]}` |
| GET | `/healthz` | – | `{"status": "ok", "model": "..."}` |

Oversized or empty batches return 422.

Embeddings are `sentence-transformers/all-MiniLM-L6-v2` with normalised output,
so cosine similarity is a dot product and pgvector's `<=>` is `1 - similarity`.
Tagging is zero-shot: the text's embedding is compared with fixed descriptions
of the ten domains and eight process themes, and up to three labels scoring at
least `TAG_THRESHOLD` (default 0.25) are returned.

## Who calls it

- `EmbedRevision` job: embeds every new problem revision (title + current
  process + pain) into `problem_revisions.embedding`.
- `/search?q=` and `POST /new/similar`: embed the query with a 1.5 s timeout;
  on any failure the page falls back (trending list / no duplicate hint).
- `ClusterNightly` does not call the service; it works on stored embeddings.

With `ML_URL` empty the Go side treats ML as disabled and all of the above
become no-ops.

## Local

```sh
cd ml
python3 -m venv .venv
.venv/bin/pip install --index-url https://download.pytorch.org/whl/cpu torch   # optional: CPU-only torch
.venv/bin/pip install -r requirements.txt
.venv/bin/uvicorn app.main:app --port 8000     # or `make ml` from the repo root
.venv/bin/pytest -q tests                       # uses a fake encoder; no model needed
```

The tests inject a deterministic fake encoder through `create_app(encoder=...)`,
so they only need fastapi, pydantic, numpy, httpx and pytest.

## Deploy

```sh
cd ml && fly deploy
```

`fly.toml` has no public services; the app is reachable only on Fly's private
network at `http://unsolved-ml.internal:8000`. Set that as the web app's
`ML_URL` secret. The model is downloaded at image build time, so the machine
needs no outbound network at boot.

# Unsolved — Build Doc

Sep 30, 2026 · @NJ

This is the spec. The coding agent implements it; it does not make design decisions.

**Rules for the coding agent**

1. Do not add any dependency, table, column, route or config key not named in this doc. If one seems needed, stop and ask.
2. Do not rename anything in this doc. Table, column, route and package names are final.
3. All SQL goes through sqlc queries in `internal/store/queries/`. No SQL strings anywhere else.
4. Handlers never touch the database directly. Handler → service → store, always.
5. Every schema change is a new goose migration. Never edit a migration that has been committed.
6. A phase is done only when its acceptance criteria pass and `make check` is green.
7. When this doc and existing code disagree, this doc wins. When this doc is silent, ask — don't guess.

## Decisions made after the doc (approved by NJ, 2026-09-30)

- **Go 1.26** instead of 1.23: every current release of river, goose, x/oauth2 and x/time requires Go ≥ 1.26, and 1.23 is end-of-life.
- **River's schema** is shipped as goose migration `0003_river.sql` (dumped with `river migrate-get`), so goose owns every schema change.
- **Signing key** for the PKCE state cookie, the double-submit CSRF cookie and the phase-3 one-tap email tokens is derived with HMAC-SHA256 from the existing secrets (`LINKEDIN_CLIENT_SECRET`, `X_CLIENT_SECRET`, `POSTMARK_TOKEN`, `DATABASE_URL`) via `config.SigningKey(purpose)` — no new config key. Rotating any of them invalidates outstanding tokens.
- **Admins and Sentry** use `ADMIN_HANDLES` and `SENTRY_DSN` (named in the Abuse section).
- **Phase 4 community state vote:** table `problem_state_votes` (migration 0005). Only `domain_contributor` and `domain_expert` in the problem's domain may vote Invalid; any signed-in non-author may vote Solved, but only once the poster has been silent (no state change, no revision by the poster) for 14 days. A state flips when the summed weight of votes reaches the threshold in `RANK_WEIGHTS_JSON` (`state_vote_threshold`, default 10).
- **Phase 5:** tables `merge_suggestions` and `trending_problems` (migration 0006). Duplicate check on the posting form is a separate htmx request fired after the user types, with a 1.5 s ML timeout that fails open; page renders never call ML. Semantic search page is `GET /search?q=`, duplicate check is `POST /new/similar`.

## Stack (final)

| Layer | Choice |
| --- | --- |
| Language | Go (see decision above) |
| HTTP router | `go-chi/chi` v5 |
| Database | PostgreSQL 16 with `pgvector` extension enabled |
| DB driver | `jackc/pgx` v5 (pgxpool) |
| Queries | `sqlc` |
| Migrations | `pressly/goose` v3, SQL files |
| Background jobs | `riverqueue/river` (Postgres-backed queue); jobs commit in the same tx as the write |
| Templates | `a-h/templ` |
| Interactivity | htmx 2 (served locally, no CDN) |
| CSS | Tailwind CSS v4 standalone CLI |
| Auth | `coreos/go-oidc` v3 + `golang.org/x/oauth2` — LinkedIn (OIDC) and X (OAuth 2.0 PKCE) |
| Sessions | Server-side in Postgres, opaque cookie; no JWTs |
| Email | Postmark API over HTTP (transactional only) |
| Logging | `log/slog`, JSON |
| Metrics | Prometheus `/metrics` |
| Config | Env vars loaded once into a typed struct |
| Hosting | Fly.io `lhr`, 2 machines; Neon `eu-west-2` (direct endpoint, pgxpool max 20, no scale-to-zero) |
| ML service (phase 5) | Python 3.12, FastAPI, `sentence-transformers`; stateless: text in, vectors/tags out |

No Redis at launch. Rejected: React/Next.js, gRPC, an ORM.

## Conventions

- IDs: UUIDv7 everywhere (`uuid.NewV7()`), generated in Go.
- Timestamps: `timestamptz`, UTC, names end `_at`.
- Text limits enforced in both Go validation and a Postgres `CHECK`.
- Errors: services return typed errors (`ErrNotFound`, `ErrForbidden`, `ErrValidation{Field, Msg}`, `ErrConflict`); one middleware maps them to HTTP status.
- Every write that also enqueues a job does both in one transaction (`river.InsertTx`).
- Soft-delete only via state (`invalid`); no DELETE on user content.
- `make check` = `go vet`, `staticcheck`, `sqlc diff`, `templ generate` check, `go test ./...`.
- Formatting: `gofmt`; no other linters.

## Data model

Migration `migrations/0001_init.sql` is the schema, verbatim from the doc. A problem is a stable identity; its text lives only in revisions, `current_revision_id` points at the leader. Solutions work the same way. Votes attach to revisions, never to problems.

**Revision rules (`internal/service/revisions.go`)**

- Creating a problem = insert `problems` + first `problem_revisions` row + set `current_revision_id`, one transaction.
- A new revision's `parent_revision_id` = the revision the author was viewing (normally current). `why_note` is required for every non-first revision.
- Revisions are immutable. No edit, no delete. A typo fix is a new revision.
- The revision tree is shown by walking `parent_revision_id`; no recursive CTE on hot paths (only on "How this evolved").
- **Fork:** new `problems` row with `forked_from_revision_id` set and a first revision copied from the chosen revision's text. Both problems link to each other. Votes are not copied.
- Domains (seed `0002_domains.sql`): manufacturing, healthcare, logistics, retail, professional_services, hospitality, construction, education, nonprofit, other.
- An author cannot vote on their own revision (`ErrForbidden`).

## Ranking and current version

Stored score = Σ w_v · 2^((t_v − t0)/h), t0 = 2026-01-01 UTC, h = half-life (30 days, `RANK_HALF_LIFE`). True decayed score = stored / 2^((now − t0)/h); ordering by stored = ordering by decayed. `RescoreAll` rebuilds every score from vote tables.

```go
type Scorer interface {
    Contribution(w float64, t time.Time) float64
    Display(stored float64, now time.Time) float64
}
type VoteWeigher interface {
    Weight(ctx context.Context, voterID uuid.UUID, domainID int16) (float64, error)
}
type DecayScorer struct { Epoch time.Time; HalfLife time.Duration }
```

VoteWeigher returns 1.0 at launch; the tier weigher ships in phase 4 from private `RANK_WEIGHTS_JSON`.

**Vote flow (one request):**
1. One tx: insert the vote with the voter's current weight. If the row exists, delete it instead (unvote), using its stored weight and `created_at`.
2. Same tx: `UPDATE problem_revisions SET score = score ± Contribution(w, t), vote_count = vote_count ± 1` — single-row update.
3. Same tx: enqueue `PickCurrentRevision{problem_id}` with River uniqueness on `problem_id` over 5 s.
4. Return the vote button partial. The browser has already flipped it optimistically.

**Recompute job:** read the problem's revisions ordered by stored score; leader = highest; tie → incumbent keeps it; a challenger needs `vote_count ≥ RANK_MIN_VOTES_TO_TAKE_OVER` (3). Set `current_revision_id` and `problems.score` = leader's score in one statement. Solutions use the identical job (`PickCurrentSolutionRevision`), then set the problem's `soft_solved` = top solution's current revision `vote_count ≥ SOFT_SOLVED_THRESHOLD` (5) (raw count). No decay job.

## Auth, identity and anonymity

LinkedIn or X only; no passwords. Reading needs no account; every write needs one.

1. `GET /auth/{provider}/start` → redirect with `state` + PKCE verifier in a short-lived signed cookie (10 min).
2. `GET /auth/{provider}/callback` → exchange code, fetch provider user id and profile URL.
3. Look up `identities (provider, provider_uid)`. Found → sign in. Not found → create `users` + `identities` in one tx, then `/welcome`.
4. `/welcome` asks handle and directory opt-in (default off). Then back to where they came from.
5. Session: 32 random bytes in cookie `us_session` (`HttpOnly`, `Secure`, `SameSite=Lax`, 30 days); store only SHA-256 in `sessions`. Sign-out deletes the row.

Scopes: LinkedIn `openid profile`; X `users.read tweet.read`. No email from providers. Email (phase 3) asked separately in settings.

CSRF: every POST carries a token from a double-submit cookie; htmx sends it via `hx-headers` set once in the layout.

**Anonymity rules (enforced in `internal/views`)**
- Every authored row has `author_id` and `author_display` (named/anonymous, per post).
- One function renders authorship: `views.Author(row, viewer)`. Named → handle linking to profile. Anonymous → "Anonymous" + the author's domain tier, no link. Templates never read `author_id` directly.
- Anonymous contributions are omitted from the author's public profile. Reputation still accrues.
- Post form toggle "Posting as @handle / Posting anonymously", defaulting to the user's last choice.
- Profile pages show social links only if `in_directory = true`.
- Logs never contain `author_id` together with the content id of an anonymous post at INFO level.

## Abuse, privacy and operations

Rate limits — in-process `golang.org/x/time/rate`, keyed by user id (IP when signed out). Over limit → 429 + plain message, WARN log.

| Action | Limit |
| --- | --- |
| New problem | 3/day |
| New revision (problem or solution) | 20/day |
| New solution | 10/day |
| Vote / unvote | 120/hour |
| Report | 20/day |
| Sign-in attempts (per IP) | 20/hour |

Moderation: admins listed in `ADMIN_HANDLES`. Any signed-in user can report a revision or user (`POST /report`). `/admin` lists unresolved reports; admin can mark a problem Invalid, suspend a user (`suspended_at`), or zero a user's vote weights and run `RescoreAll` for affected problems. Every admin action writes a `problem_state_events` row or a WARN log with the admin's id.

Privacy (UK GDPR): `/settings/export` returns own data as JSON including anonymous posts. `/settings/delete` deletes identities and sessions, scrubs users (handle → `deleted-<short id>`… note handle CHECK is `^[a-z0-9_]{3,24}$` so use `deleted_<8 hex>`; name, history, email cleared, `deleted_at` set). Content stays, shown as "deleted user". Privacy page states what provider data is stored (id and profile URL only), that anonymous posts are hidden from users but not from the platform, and how deletion works.

Operations: Sentry (`SENTRY_DSN`), panics recovered and reported; graceful shutdown (SIGTERM → stop accepting, 10 s in-flight, stop River cleanly); alerts: error rate > 1% for 5 min, p95 > 500 ms for 10 min, any job discarded.

## Routes

Every route returns full HTML normally and a partial when `HX-Request` is present. No JSON API. Lists paginate by keyset (`?after=<score>_<id>`), 30 per page, never OFFSET.

| Method | Path | Auth | Returns |
| --- | --- | --- | --- |
| GET | `/` | – | Open problems, not soft-solved, by score; seed problems pinned on top until 50 non-seed problems exist |
| GET | `/problems?state=&domain=&sort=top\|new` | – | Filtered list |
| GET | `/new` | yes | Guided posting form |
| POST | `/problems` | yes | Create problem + first revision → redirect |
| GET | `/p/{id}` | – | Problem page: current revision, solutions by score, founder-interest count, state history |
| GET | `/p/{id}/evolution` | – | All revisions as a tree, each with why-note and score |
| GET | `/p/{id}/revise` | yes | Form prefilled from current revision |
| POST | `/p/{id}/revisions` | yes | New revision (requires `parent_revision_id`, `why_note`) |
| POST | `/p/{id}/fork` | yes | Fork from `revision_id` → redirect to new problem |
| POST | `/r/{revision_id}/vote` | yes | Toggle vote → vote button partial |
| POST | `/p/{id}/solutions` | yes | New solution + first revision |
| POST | `/s/{id}/revisions` | yes | New solution revision |
| POST | `/sr/{revision_id}/vote` | yes | Toggle vote → partial |
| POST | `/s/{id}/tried` | yes | Record outcome (worked/partly/failed + note) |
| POST | `/p/{id}/state` | yes | Poster marks solved/reopens; phase 4 adds weighted votes for Invalid |
| POST | `/p/{id}/interest` | yes | Toggle founder interest |
| GET | `/u/{handle}` | – | Profile: named contributions only; social links if in directory |
| GET | `/members` | – | Directory of opted-in users |
| GET | `/meta` | – | Meta board (`on_meta_board = true`) |
| GET/POST | `/settings` | yes | Handle, directory opt-in, declared history, email |
| GET | `/healthz`, `/metrics` | – | Liveness; Prometheus (metrics on internal port 9091) |

Also named elsewhere in the doc: `/auth/{provider}/start`, `/auth/{provider}/callback`, `/welcome`, `POST /report`, `/admin`, `/settings/export`, `/settings/delete`, privacy page (`/privacy`), about page (`/about`, states that ranking thresholds exist and are private), sign-out (`POST /auth/signout`), sign-in chooser page (`GET /signin?next=`).

**Guided posting form** (`/new`), fields in this order: 1. Domain (select, required) 2. "Describe how this is done today, step by step" → `current_process` 3. "What goes wrong or costs time?" → `pain` 4. "What have you already tried?" → `tried` (may be "nothing yet") 5. "One-line title" → `title` 6. Posting-as toggle. There is no field asking what should be built.

**Page budget:** < 200 ms p95 server time; at most 3 queries per page.

## Background jobs

River in-process, queue `default`, 10 workers per machine. Every job idempotent. Retry up to 5 attempts, then discarded and logged at ERROR.

| Job | Trigger | Does | Phase |
| --- | --- | --- | --- |
| `PickCurrentRevision` | Problem revision vote; unique per problem for 5 s | Pick current version from stored scores | 1 |
| `PickCurrentSolutionRevision` | Solution revision vote; unique per solution for 5 s | Pick current, update `soft_solved` | 2 |
| `RescoreAll` | Manual (admin) | Rebuild every score from vote tables, 500 rows per batch | 1 |
| `SessionSweep` | Periodic, hourly | Delete expired sessions | 1 |
| `SolvedPrompt` | Solution score crosses `SOFT_SOLVED_THRESHOLD` first time | Email poster one-tap worked/partly/didn't link (signed token, 14-day expiry) | 3 |
| `RecomputeStanding` | Problem marked solved; revision becomes current; nightly full pass | Update `user_domain_standing` points and tier | 4 |
| `EmbedRevision` | New problem revision | Call ML service, store `embedding` | 5 |
| `ClusterNightly` | Periodic, 02:00 UTC | Cluster embeddings, write merge suggestions + trending | 5 |

**Never on the hot path:** COUNT(*) over large tables (use stored `vote_count`), OFFSET pagination, recursive queries, computing scores at read time, calling the ML service during a request.

## Testing, CI, deploy, config

- Unit tests for `internal/ranking` and `internal/service`, table-driven. Ranking covers: decay halves at exactly one half-life; tie keeps incumbent; takeover blocked under minimum votes.
- Store and service tests run against real Postgres via `testcontainers-go` (`pgvector/pgvector:pg16`), migrations applied fresh per package (`internal/store/storetest`). No DB mocks.
- One end-to-end test per phase through the router with `httptest`.
- CI: GitHub Actions `make check` on every push; on `main` also `flyctl deploy`. Migrations run as Fly `release_command`.
- Local: `docker compose up` (Postgres 16 + pgvector); `make dev` = templ watch + Tailwind watch + `air`.

Config (complete list): `DATABASE_URL`, `BASE_URL`, `SESSION_TTL` (720h), `LINKEDIN_CLIENT_ID`/`LINKEDIN_CLIENT_SECRET`, `X_CLIENT_ID`/`X_CLIENT_SECRET`, `POSTMARK_TOKEN`, `RANK_HALF_LIFE` (720h), `RANK_MIN_VOTES_TO_TAKE_OVER` (3, private), `SOFT_SOLVED_THRESHOLD` (5, private), `RANK_WEIGHTS_JSON` (private), `ML_URL` (phase 5), plus `SENTRY_DSN` and `ADMIN_HANDLES`.

## Build phases

**Phase 0 — Skeleton:** repo layout; `make check` green; migrations apply and roll back; `/healthz`; Fly + Neon deploy; CI deploys on `main`.

**Phase 1 — Problems, revisions, voting, sign-in:** LinkedIn and X sign-in end to end, `/welcome`; guided form creates problem + first revision in one tx; revise creates child revision, missing why-note rejected; voting toggles, optimistic, blocks self-votes, single-row score update; later revision with higher score and ≥3 votes becomes current within 10 s; `/p/{id}/evolution` tree; fork creates linked problem; anonymous posts never show the handle anywhere incl. profile; front page lists open problems by score, keyset-paginated; rate limits enforced; suspended users cannot write.

**Phase 2 — Solutions, states, launch content:** solutions with kind, revisions, votes (identical mechanics); "Tried it" outcomes shown; poster marks Solved/reopens, every change writes `problem_state_events`; `soft_solved` from top solution's vote count, front page excludes soft-solved; founder interest toggle + count; members directory; meta board; reports, `/admin`, export, deletion, privacy page; Sentry, alerts, backup rehearsal; five seed problems loaded by `cmd/seed` with `is_seed = true`, pinned.

**Phase 3:** optional email in settings; `SolvedPrompt` email with one-tap outcome link.

**Phase 4:** `RecomputeStanding` and tier-weighted `VoteWeigher` from private config; vouching; weighted community vote for Solved (poster silent) and Invalid; tier shown beside names and on anonymous posts.

**Phase 5:** `ml/` FastAPI: `POST /embed` (text → 384-dim, `all-MiniLM-L6-v2`), `POST /tag`; `EmbedRevision`; semantic search page; duplicate check on posting form; `ClusterNightly` for trending + merge suggestions.

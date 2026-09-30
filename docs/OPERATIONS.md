# Unsolved — Operations runbook

How production runs, how to deploy and roll back, how to restore the database,
and which alerts exist. The spec is `docs/BUILD.md`; this file does not change it.

Steps marked **[NJ — manual]** need account access (Fly, Neon, Sentry,
Cloudflare, GitHub) and have to be done by NJ. Everything else is automated or
is a command anyone with access can run.

## At a glance

| Piece | Where | Notes |
| --- | --- | --- |
| App | Fly.io app `unsolved`, region `lhr`, 2 machines | HTTP on :8080, Prometheus metrics on :9091 (internal only) |
| Database | Neon, `eu-west-2`, **direct** endpoint | Postgres 16 + pgvector, pgxpool max 20 per machine, no scale-to-zero, 7-day PITR |
| Jobs | River, in-process in the app | queue `default`, 10 workers per machine, 5 attempts then discarded |
| Errors | Sentry (`SENTRY_DSN`) | panics recovered and reported; 500s reported |
| Edge | Cloudflare in front of Fly | caches `/static/*` only (at launch) |
| CI/CD | GitHub Actions | `make check` on every push; `flyctl deploy` on `main` |

## Fly.io

### First-time setup **[NJ — manual]**

1. `fly apps create unsolved` (or `fly launch --no-deploy` using the committed `fly.toml`).
2. Set secrets (below), then deploy once by hand: `fly deploy --remote-only`.
3. Make sure there are exactly two machines in `lhr`:
   `fly scale count 2 --region lhr` then `fly status` (both `started`, both passing `/healthz`).
   `fly.toml` keeps `min_machines_running = 2` and `auto_stop_machines = "off"`.
4. Add `FLY_API_TOKEN` to the GitHub repo secrets (`fly tokens create deploy -x 999999h`)
   so the `deploy` job in `.github/workflows/ci.yml` can run on `main`.
5. Custom domain: `fly certs add <domain>` and point Cloudflare DNS at the app (see Cloudflare).

### Secrets **[NJ — manual]**

Set with `fly secrets set KEY=value ...` (setting secrets restarts the machines).
`SESSION_TTL` and `RANK_HALF_LIFE` are plain `[env]` values in `fly.toml`.

| Secret | Value |
| --- | --- |
| `DATABASE_URL` | Neon **direct** connection string (host without `-pooler`), `sslmode=require` |
| `BASE_URL` | `https://<domain>` (no trailing slash) |
| `LINKEDIN_CLIENT_ID`, `LINKEDIN_CLIENT_SECRET` | LinkedIn app (OIDC, scopes `openid profile`) |
| `X_CLIENT_ID`, `X_CLIENT_SECRET` | X app (OAuth 2.0 PKCE, scopes `users.read tweet.read`) |
| `POSTMARK_TOKEN` | Postmark server token (phase 3) |
| `RANK_MIN_VOTES_TO_TAKE_OVER` | private (default 3) |
| `SOFT_SOLVED_THRESHOLD` | private (default 5) |
| `RANK_WEIGHTS_JSON` | private JSON (phase 4) |
| `SENTRY_DSN` | from the Sentry project |
| `ADMIN_HANDLES` | comma-separated handles, e.g. `nj` |
| `ML_URL` | phase 5 only |

Rotating `LINKEDIN_CLIENT_SECRET`, `X_CLIENT_SECRET`, `POSTMARK_TOKEN` or
`DATABASE_URL` changes the derived signing key: outstanding sign-in state
cookies, CSRF cookies and emailed one-tap links become invalid (users just
reload / sign in again).

### Deploys and migrations

- Push to `main` → CI runs `make check` → `flyctl deploy --remote-only`.
- `fly.toml` `[deploy] release_command = "/app/migrate up"` runs goose migrations
  in a temporary machine **before** any new machine starts. If the migration
  fails, the deploy stops and the old machines keep serving.
- Strategy is `rolling`: one machine at a time, each must pass `/healthz`.
- Migrations must stay backwards compatible with the previous release (the old
  machine keeps running while the new one starts): add columns/tables first,
  use them in a later deploy; never rename or drop in the same deploy.
- Useful: `fly releases`, `fly logs`, `fly status`, `fly ssh console`.

### Rollback

1. Find the last good release: `fly releases --image` (note its image tag).
2. Redeploy it: `fly deploy --image registry.fly.io/unsolved:<tag>`.
   The release command runs `migrate up` again, which is a no-op.
3. If a migration itself was bad (rare; migrations are additive), roll it back
   explicitly with the old image: `fly ssh console -C "/app/migrate down"`
   (one step) and check `fly ssh console -C "/app/migrate status"`.
   For anything destructive, restore from Neon instead (below).
4. Revert the bad commit on `main` so the next CI deploy doesn't reintroduce it.

### Seeding launch content

After the first deploy, load the five seed problems once (idempotent, safe to re-run):

```sh
fly ssh console -C "/app/seed"
```

## Neon (Postgres)

### Settings **[NJ — manual]**

- Region `eu-west-2` (London), Postgres 16. Enable the extension once:
  migrations run `CREATE EXTENSION IF NOT EXISTS vector`, which Neon allows.
- **Use the direct endpoint, not the pooler.** River uses `LISTEN/NOTIFY` and
  session-level state, which PgBouncer in transaction mode breaks. The direct
  host is the one *without* `-pooler` in its name.
- pgxpool max is 20 connections per machine (set in code), so 2 machines use up
  to 40 plus one for the release command. Pick a compute size whose
  `max_connections` comfortably exceeds that.
- **Scale to zero: off** (compute "Suspend compute after inactivity" disabled),
  so River's leader and `LISTEN` connections stay up and the first request has no cold start.
- **History retention (PITR): 7 days.**

### Backup restore rehearsal (to a branch) **[NJ — manual]**

Do this before launch and then once a quarter. It proves we can get data back
from a point in time without touching production. Tick each box and note the date.

1. Pick a restore point: a timestamp within the last 7 days, e.g. one hour ago.
   Note a fact you can check at that point, e.g.
   `SELECT count(*) FROM problems WHERE created_at < '<timestamp>';` on production.
2. In the Neon console: **Branches → Create branch**, parent `main`,
   "Point in time" = the timestamp, name `restore-rehearsal-YYYYMMDD`.
   (CLI alternative: `neonctl branches create --name restore-rehearsal-YYYYMMDD --parent '<timestamp>'`;
   check `neonctl branches create --help` for the current flag syntax.)
3. Copy the branch's **direct** connection string (no `-pooler`).
4. Check the schema version:
   `DATABASE_URL='<branch url>' go run ./cmd/migrate status` — all migrations applied, same as production.
5. Check the data:
   `psql '<branch url>' -c "SELECT count(*) FROM problems; SELECT count(*) FROM users; SELECT max(created_at) FROM problem_revisions;"`
   The `problems` count matches step 1; no revision is newer than the timestamp.
6. Check the app runs on it: in a local checkout, `DATABASE_URL='<branch url>' BASE_URL=http://localhost:8080 make run`
   and open `/`, a problem page and `/about`.
7. Write down how long steps 2–6 took (this is our restore time).
8. Delete the branch (Neon console → branch → Delete) so it doesn't cost compute.

Checklist:

- [ ] Restore point chosen and expected count noted
- [ ] Branch created from point in time
- [ ] `migrate status` shows every migration applied
- [ ] Row counts match the restore point
- [ ] App served pages from the branch
- [ ] Restore time recorded: ____ minutes
- [ ] Branch deleted
- [ ] Date of rehearsal: __________

**A real restore** follows the same steps, then either (a) points production at
the branch — `fly secrets set DATABASE_URL='<branch direct url>'` — or (b) uses
Neon's "Restore" on `main` to the timestamp (which keeps a backup branch of the
pre-restore state). Prefer (b) when the whole database must go back; use (a) or
copy selected rows from the branch when only some data was damaged.

## Sentry **[NJ — manual]**

1. Create a Go project in Sentry (EU data region if available).
2. `fly secrets set SENTRY_DSN=<dsn>`.
3. The server initialises Sentry only when `SENTRY_DSN` is set. It reports
   recovered panics and every request that ends in a 500.
4. Alert rule: "A new issue is created" and "An issue changes from resolved to
   unresolved" → email NJ.

## Alerts

Fly's managed Prometheus scrapes `[metrics]` (port 9091, `/metrics`). Query it
from Fly's managed Grafana (`fly dashboard metrics`, or fly-metrics.net) and
create Grafana alert rules there, notifying email. **[NJ — manual]** to create
the rules and the contact point.

The only request metric is the histogram `http_request_duration_seconds` with
labels `method`, `route` (chi route pattern), `status`. Fly adds `app`,
`region` and `instance` labels.

### 1. Error rate > 1% for 5 minutes

```promql
sum(rate(http_request_duration_seconds_count{app="unsolved", status=~"5.."}[5m]))
/
sum(rate(http_request_duration_seconds_count{app="unsolved"}[5m]))
> 0.01
```

Grafana rule: evaluate every 1m, **pending period 5m**.

### 2. p95 latency > 500 ms for 10 minutes

```promql
histogram_quantile(0.95,
  sum by (le) (rate(http_request_duration_seconds_bucket{app="unsolved"}[5m]))
) > 0.5
```

Grafana rule: evaluate every 1m, **pending period 10m**. (There is a 0.5 s
bucket boundary, so this threshold is exact.)

### 3. Any job discarded after retries

River retries a job up to 5 attempts; on the last failure the server logs, at
ERROR, a JSON line with `"msg":"job discarded after retries"` plus `kind`,
`job_id`, `attempt` and `err`. Panicking jobs log `"msg":"job panicked"`.

Set an alert on that log line **[NJ — manual]**:

1. Ship Fly logs to a log service with alerting (e.g. deploy
   [fly-log-shipper](https://github.com/superfly/fly-log-shipper) to Better Stack,
   Grafana Loki or Axiom).
2. Alert on any line where `level = "ERROR"` and `msg = "job discarded after retries"`
   (count > 0 over 5 minutes) → email NJ.

Until log shipping exists, check by hand after deploys:
`fly logs | grep 'job discarded after retries'`, and in SQL:
`SELECT kind, count(*) FROM river_job WHERE state = 'discarded' AND finalized_at > now() - interval '1 day' GROUP BY kind;`

## Cloudflare **[NJ — manual]**

1. Add the domain to Cloudflare; DNS record for the app is **proxied** (orange
   cloud) and points at the Fly app (`fly certs add` shows the target; use a
   CNAME to `unsolved.fly.dev`).
2. SSL/TLS mode **Full (strict)**. Always Use HTTPS on.
3. Cache rule: URI path starts with `/static/` → eligible for cache, edge TTL
   1 day, respect origin for browser TTL (origin sends `Cache-Control: public, max-age=3600`).
   After changing `static/` files, purge `/static/*` (or just wait out the TTL).
4. Everything else: **bypass cache** (default for HTML). Don't enable Rocket
   Loader or HTML minification: they interfere with htmx.
5. The app reads the client IP from `CF-Connecting-IP` (rate limits key on it
   when signed out), so keep Cloudflare in front; don't expose the `fly.dev`
   hostname publicly.

### Scale stage (not at launch): 30 s caching of anonymous HTML

When read traffic warrants it, cache anonymous HTML pages for 30 s at the edge:

- Only for requests **without** the `us_session` cookie, and only `GET`.
- Pages already send `Vary: HX-Request`, but Cloudflare ignores most `Vary`
  headers, so the cache rule must also **exclude requests with an `HX-Request`
  header**, or partials could be served as pages (and pages as partials).
- Prerequisite code change (ask first; not built): anonymous pages currently set
  the `us_csrf` cookie and embed its token in the page, and Cloudflare won't
  cache responses with `Set-Cookie`. The CSRF cookie would need to be issued on
  sign-in pages / non-cacheable responses only.

## Routine checks

- After each deploy: `fly status` (2 machines healthy), glance at Sentry.
- Weekly: `/admin` for open reports; the discarded-jobs SQL above.
- Quarterly: backup restore rehearsal.
- Admin actions (suspend, zero votes, meta board, rescore, resolve) log at WARN
  with `"msg":"admin action"` and `admin_id`; Invalid/reopen write
  `problem_state_events` rows with `actor_id` = the admin.

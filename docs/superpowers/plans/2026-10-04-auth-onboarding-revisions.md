# Authentication, Onboarding and Revisions Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development to implement and review each scoped task.

**Goal:** Add Google, GitHub and Reddit sign-in, contributor preferences and discoverable, comparable problem revisions.

**Architecture:** Extend the Go service, existing OAuth/session flow and immutable revision system. Use additive database migrations and server-rendered responsive forms. Provider accounts stay distinct; no automatic linking.

**Tech Stack:** Go, chi, pgx, sqlc, goose, templ, Tailwind, existing oauth2 and OIDC libraries.

**Spec:** docs/auth-onboarding-revisions-design.md (approved by user).

## Global Constraints

- Preserve existing authentication, CSRF, safe redirects, moderation and ranking rules.
- Only configured providers appear; live credentials and Reddit approval are external prerequisites.
- Preferences do not grant permissions. Original return paths survive onboarding.
- Revisions remain immutable; challenger needs configured minimum votes and a higher score.
- Desktop/mobile, keyboard access, privacy export and deletion remain supported.

## Review Focus

- A second provider with a matching name must not take over an existing account (Task 1).
- Cancelled, mismatched, expired or malformed OAuth responses must not create sessions (Task 1).
- Skipping onboarding or submitting bad preferences must not lose a correction return path (Task 2).
- Signed-out, suspended and invalid-problem states must offer accurate actions (Task 3).
- Comparisons must use each revision's actual parent, including branches (Task 3).

## Task 1: Provider support

Files: internal/config/config.go; internal/auth/{oauth,providers}.go and new provider adapter/tests; internal/http/auth.go and auth_test.go; internal/views/pages/signin.templ; provider labels in service/views; migrations/0007_auth_providers.sql; .env.example; docs/AUTH-SETUP.md.

Interface: preserve ParseProvider(string), Auth.Enabled(store.Provider), Auth.StartSignIn and FinishSignIn. Use provider IDs google, github, reddit. Keep onboarding redirect logic unchanged for Task 2.

- [ ] Extend fake-provider tests with successful identity exchange, unavailable credentials, rejected state/token, distinct provider identities.
- [ ] Run focused auth tests and confirm missing-provider failures.
- [ ] Add enum values and configuration, provider-specific code exchange and profile validation, parameterized sign-in options and generalized labels. Do not add new secrets to the legacy signing-key derivation, preserving sessions.
- [ ] Regenerate sqlc/templ output, run auth/HTTP tests, document exact callbacks and provider setup.
- [ ] Review changed code for token/secret exposure, requested scopes, signature validation and account identity boundaries.

## Task 2: Contributor onboarding

Files: migrations/0008_contributor_preferences.sql; internal/store/queries/{users,privacy}.sql; internal/service/{users,settings}.go; internal/http/{users,settings}.go; internal/auth/oauth.go; welcome/settings templates and a shared preference partial; focused HTTP/service tests.

Interface: persisted contribution_preference values '', identifier, solver, both and onboarding_completed boolean. Extend existing welcome/settings forms rather than introducing a parallel profile system. Preserve Auth callback return type.

- [ ] Add tests for preference selection, invalid choice, skip, returning account setup, safe return destination, export and deletion.
- [ ] Run focused tests before implementation to reproduce absent persistence.
- [ ] Add database fields/queries; save preference and completion transactionally with welcome profile updates. Skip preserves existing handle/profile.
- [ ] Add accessible choices, optional setup for existing accounts, and Settings editing. Route identifier to /new and solver/both to discovery only when there is no specific return destination.
- [ ] Regenerate outputs and run user/settings/auth tests. Verify existing users retain access.

## Task 3: Refinement and comparison

Files: internal/views/pages/{problem,evolution,problem_form}.templ and comparison helper/tests; internal/http/problems.go and revision tests; CSS only if required by responsive layout.

Interface: extend EvolutionView/Revision with actual parent or comparison fields and configured minimum votes. Reuse /p/{id}/revise and /p/{id}/evolution.

- [ ] Add tests for signed-out preview/full-page correction actions, redirect back to form, invalid/suspended restrictions, branched parent comparison and configured threshold text.
- [ ] Run focused tests to show current discoverability failure.
- [ ] Add visible Refine or correct and Revision history actions; reuse RequireWriter for authentication. Add before/after field comparison with unchanged fields identified, readable on mobile.
- [ ] Show current-version rule using actual configuration; explain votes as community support. Preserve ranking calculation and immutable revisions.
- [ ] Regenerate templates and run revision/HTTP tests. Review markup keyboard and screen-reader behavior.

## Integration and verification

- [ ] Review each task and resolve findings; run go test ./..., go vet, staticcheck, sqlc and templ deterministic-generation checks, git diff --check.
- [ ] Build CSS and server, apply additive migrations to local development database, restart and smoke-test pages.
- [ ] Inspect desktop and mobile sign-in, onboarding and problem/revision layouts with available browser tooling.
- [ ] Report implementation and verified flows separately from live provider setup.

## Execution record

User approved the design and explicitly requested implementation. Proceed without another permission gate. Preserve pre-existing login fixes. No push, merge or deployment requested.

### Completion record — 2026-10-04

- Task 1 implemented: Google, GitHub and Reddit adapters, provider gating,
  profile labels, privacy copy, setup guide, additive enum migration and fake
  provider regression coverage. Existing signing-key derivation preserved.
- Task 2 implemented: identifier/solver/both choices, persisted preferences and
  completion, optional existing-account setup, skip, Settings, export and
  deletion. Independent review found no actionable issues.
- Task 3 implemented: discoverable correction/history controls, before/after
  comparison, current-version rule from config. Independent review found no
  actionable issues; focused tests passed.
- Final review identified Reddit's missing operator contact header. Added
  REDDIT_USER_AGENT configuration, documented the quoted format, tested the
  exact header in token and profile calls. Regression reproduced before the
  fix and passed afterward. Scoped re-review confirmed resolution.
- Full Go suite, vet, staticcheck and deterministic SQL/template generation
  passed. Applied local migrations through version 8 and rebuilt the server.
- Browser review used rendering-only fixtures for authenticated onboarding and
  all-provider layouts (no development authentication bypass). Checked 390px
  and 1280px layouts, keyboard radio selection, mobile comparison expansion
  and horizontal overflow. Checked real local problem correction visibility
  and the sign-in return path.
- Live third-party authentication is not verified: local credentials remain
  unconfigured. The setup guide lists every required setting and callback.
- No changes to ranking math, permission roles, or account auto-linking.

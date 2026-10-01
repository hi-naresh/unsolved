# Process Atlas implementation plan

Goal: implement the approved responsive redesign in REDESIGN-PROPOSAL.md.
Architecture: retain server-rendered templ pages, shared Tailwind components and progressively enhanced vanilla JavaScript. Existing routes, data contracts and privacy rules stay authoritative.

## Constraints and review focus
No new dependencies, routes or database changes. Preserve existing Makefile and compose edits. Test narrow screens, long titles, failed/rapid preview requests, no-JavaScript reading, and authenticated form/author states.

## Tasks
- [x] Restore the local runtime from this checkout; baseline Go tests and assets.
- [x] Replace tokens and shell in static/css/input.css and internal/views/layouts/base.templ; add responsive navigation, clear active states and shared controls.
- [x] Redesign home.templ and problem_rows.templ; improve discovery, selection and previews in static/js/app.js. Verify latest-request wins, retry and mobile links.
- [x] Redesign problem.templ, solution_card.templ and evolution.templ around workflow and evidence; retain all permission checks.
- [x] Improve form, onboarding, search, profile, settings, admin and document pages using shared layout and scoped components. Resolve conflicting meter styles.
- [x] Regenerate templates/CSS; run Go checks and responsive browser inspection; review and fix regressions; update docs/DESIGN.md.

## Execution ledger
User explicitly requested implementation now following proposal approval; proceed inline without another approval gate. Work on codex/process-atlas in the current checkout so the local preview reflects changes and existing user environment is preserved.
Asset diagnosis: listener PID 52732 has cwd /Users/naresh/.Trash/uns/unsolved; assets return 403. Restart from current checkout rather than weakening request security.

## Verification results

- Full `go test ./...` passed after updating assertions for the deliberately revised search fallback and "Likely solved" wording. Focused search and solved-state integration tests also passed.
- `go vet ./...`, staticcheck and sqlc diff passed. Template regeneration was byte-for-byte stable, CSS and server builds passed, and `git diff --check` was clean. The Makefile's templ-check requires committed changes; the equivalent generation comparison was used while retaining uncommitted work for review.
- Both JavaScript regression tests passed: latest-request wins and failed-preview retry/full-page fallback.
- Browser checks: explorer and problem reading at 360/390px; responsive explorer at 768/1024/1440px; no horizontal document overflow in those views. Verified light/dark appearance, desktop selection, mobile full-page navigation and expanded-state accessibility.
- Contribution preview populated correctly; a collapsed preview remained reopenable after resizing to desktop. Form and settings narrow layouts were inspected using temporary, isolated rendered fixtures without database access or live submissions. Fixture server stopped and its temporary source removed afterward.
- Independent review identified two issues, both fixed: vote labels now include counts, and form-preview summaries remain accessible at every size.
- Shared styles reach all page families. This is not a claim of exhaustive manual coverage of every signed-in or moderator state; existing integration tests provide backend/permission regression coverage. Real provider sign-in and production deployment were not performed.

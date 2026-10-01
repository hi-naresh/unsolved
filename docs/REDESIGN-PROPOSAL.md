# Unsolved: desktop and mobile redesign proposal

Date: 2026-10-01. Status: proposed for review; product implementation has not started.

## Intent and success criteria

The request is to audit the whole platform, plan a distinctive and interactive redesign, then implement it for web and mobile. The existing product is a community for operational problems: describing today's manual work, comparing proposed improvements, and collecting evidence about whether those improvements work.

Assumption: mobile means the responsive web platform, not a separate native application. The primary audience is people doing operational work and people who can improve it. A visitor should understand the pain, the current process, and the next useful action without having to learn the site's mechanics.

Success means that all existing pages share a coherent visual language; discovery, contribution and evaluation work comfortably at phone and desktop sizes; keyboard and no-JavaScript paths remain usable; and authorship privacy, voting eligibility and existing backend rules remain intact.

## Evidence from the audit

- The live local server at localhost:8080 returns 403 for both /static/css/app.css and /static/js/app.js. The browser renders unstyled content and an oversized logo. Establish the cause and restore asset delivery before judging the existing design visually. The files exist locally; the exact cause is not yet established.
- Main navigation links disappear below the desktop breakpoint without replacement navigation. The brand name also disappears at small sizes.
- The explorer already has a valuable list/detail model, regular links on mobile, and desktop keyboard navigation. Preserve these capabilities.
- The page templates give cards, chips, metadata and controls similar emphasis. Reorganize hierarchy around the problem and its evidence.
- Several secondary controls use 11–13px type and short hit areas. Increase interactive targets without inflating passive metadata.
- Desktop selection changes before the preview request succeeds. There is no explicit preview retry UI; slow or failed requests need an honest state.
- Disclosure controls change text without updating aria-expanded. Clamped content needs a usable fallback without JavaScript.
- Forms already offer structured questions, duplicate suggestions, progress and live preview. Improve these foundations instead of replacing them with an opaque wizard.
- forms.css and pages.css both define .meter and .btn-danger with different meanings. Split these into semantic components to prevent accidental styling conflicts.
- Two unrelated local files, Makefile and docker-compose.yml, already have modifications. Preserve them.

## Direction and alternatives

Recommended: **Process Atlas**. A workspace built around the path from manual work to demonstrated improvement. The signature element is the actual numbered workflow, with solutions and outcome evidence next to it. Connections encode real relationships, never arbitrary decoration.

Alternative: a community feed. Familiar and efficient for scanning, but makes processes and evidence feel like ordinary posts.

Alternative: a graph-first canvas. Strong visual identity, but introduces navigation complexity and poor small-screen readability. Keep graphs as optional views inside the atlas.

## Visual system

- Canvas: mist #F3F6F8. Surface: white #FFFFFF. Ink: navy #172B3A. Secondary text: steel #526675. Primary action: ocean #006D77. Selected surface: pale aqua #E1F2F2.
- Semantic colors stay distinct from brand color: amber for votes/caution; green for reported success; red for errors; each solution kind retains its own labeled identity. Color is never the only signal.
- Dark theme: deep blue-gray canvas, raised blue-gray surfaces, light text and a lighter aqua accent. Audit every semantic foreground/background pair for contrast.
- Type: Avenir Next with a system sans-serif fallback; deliberately sized headings, 16px primary reading text, and 13–14px supporting information. No new font service or runtime dependency.
- Use left alignment, 60–75-character reading measures, an 8px spacing rhythm, 10px controls and 18px primary surfaces. Separate content with spacing first; borders express actual grouping. Reserve shadows for overlays and lifted selection.
- Motion: short state changes for expansion, selection and feedback. Respect reduced motion; no ambient animation or arbitrary card entrances.
- Distinctiveness review: retain the workflow as the recognizable element. Avoid turning the atlas into a generic dashboard of statistics, identical cards or ornamental node diagrams.

## Component and page plan

| Area | Design and interaction changes | Mobile treatment |
| --- | --- | --- |
| App shell | Visible wordmark, clear Explore/Solved/Community navigation, search, posting action, account menu, current location, restrained footer | Compact brand/search header and labeled bottom links for Explore, Search, Post and Members; account stays accessible in header; safe-area spacing |
| Explorer | Brief product introduction, clear page title, domain selector, Open/Top/New/Solved controls, ranked problem rows and live preview | Single-column rows; native navigation to detail; readable filters with no hidden categories |
| Problem rows | Title first, concise pain excerpt, domain/state, labeled votes and solution count; selected row uses an inset accent and surface | Whole row is a generous target; metadata wraps instead of truncating essential status |
| Preview | Separate problem summary, current workflow and solution evidence; full-page action remains visible; explicit loading/error/retry states | No duplicate hidden preview content required; open the full problem |
| Problem detail | Strong title/pain hierarchy, readable process timeline, attempts made, solution count, evidence, authorship and secondary history | Section links to Process, Solutions and Status; stacked content with scroll offsets |
| Process timeline | Number only true steps; expandable long workflows; preserve original text and a plain reading fallback | Vertical timeline with no horizontal panning |
| Votes and interest | Clear pressed/disabled states; explain unavailable actions; pending and failed feedback; preserve server-confirmed counts | At least 44px primary touch targets; no hover dependency |
| Solution cards | Kind label, readable proposal, author, votes, prominent actual outcomes, then Try/Revise actions | Inline forms expand into full-width space; meaningful labels remain visible |
| Solution map | Optional supporting view with a readable legend and links to actual solutions; list remains primary | Collapsible map; cards carry all necessary information |
| New problem and revision | Structured visible steps, examples, progress, duplicate checks and live preview; validation beside fields and a summary; explicit posting identity | Single-column form with optional preview disclosure; no sticky controls covering the keyboard or fields |
| Solution forms | Four clearly explained kinds, consistent identity control and actionable validation | Large radio-card targets; body input and submit action fit narrow screens |
| Outcome confirmation | Clear connection to the solution, Worked/Partly/Didn't work choices and confirmation | Large stacked choices; existing confirmation behavior preserved |
| Evolution | Revision tree plus readable revision list, current-version emphasis and revision reason | List-first presentation with optional graph, no tiny graph as the only navigation |
| Search and similar problems | Persistent query, clear loading/results/empty/error states; similarity explained as relevance, not certainty | Full-width search and results; suggestions do not interrupt typing |
| Profile and members | Domain standing and contributions grouped clearly; declared background stays labeled unverified | Stacked profile and contribution rows; vouch controls remain labeled |
| Sign-in and welcome | Clear reason to join, trustworthy provider actions, concise handle setup and visibility choice | Comfortable single-column layout, large inputs and provider buttons |
| Settings and deletion | Profile, directory and email groups; clear save feedback; separate destructive section | Full-width controls; existing deletion confirmation retained |
| Admin and merge suggestions | Scannable report context, current state, reason and distinct action grouping | Stacked report sections; dangerous actions separated from routine resolution |
| Meta, About, Privacy and messages | Shared page headers, readable documentation, useful next steps and consistent navigation | Narrow reading measure and clean heading rhythm |
| Shared disclosures, pagination and reports | Correct expanded state, visible focus, labeled controls, reliable more-results feedback and recoverable errors | Popovers constrained to viewport; keyboard and touch support |

## Layout sketches

Desktop explorer:

```text
Wordmark     Explore  Solved  Community        Search      Post / Account
Find a better way to get the work done.                 How it works
Page title and domain filter
┌──────────────────────────┬─────────────────────────────────────────┐
│ Open / Top / New / Solved │ Selected problem title and status       │
│                          │ Pain and current workflow               │
│ Problem and pain         │   1. Receive → 2. Check → 3. Record      │
│ Votes / solutions        │                                         │
│                          │ Solutions and evidence                  │
│ Selected problem         │ Open full problem                       │
└──────────────────────────┴─────────────────────────────────────────┘
```

Mobile:

```text
Wordmark                         Search / Account
Page title
Domain filter
Open / Top / New / Solved
Problem title
Pain excerpt
Domain / votes / solutions
─────────────────────────────────────────────────
More problem rows

Explore          Search          Post          Members
```

At 1024px and above use the list/detail workspace. Below 1024px use one document flow. At 768–1023px retain the full-width content layout without forcing a cramped preview. Validate 360, 390, 768, 1024 and 1440px, including long titles and 200% zoom.

## Technical boundaries and state handling

Continue Go, templ, Tailwind, htmx and small vanilla JavaScript modules. Templates receive existing view models; mutations continue through existing handlers and services. No new schema, routes, external services or framework is required. All author rendering continues through the privacy-aware existing helpers.

Normal links and form posts remain the baseline. JavaScript enhances preview, disclosure, focus and feedback. Only the latest selected preview request may update the visible pane. Preview selection must correspond to the rendered problem; errors provide retry and open-full-page options. Form validation preserves input and does not show success before a successful server response. Disabled/pending actions must recover after errors.

Use shared shell and semantic components, scoped page layouts and separate visual tokens. Regenerate committed templ output after template edits; rebuild the CSS artifact. Update docs/DESIGN.md to describe the final system.

## Implementation sequence and review checkpoints

1. Diagnose asset delivery and establish a styled local baseline; run existing checks to separate pre-existing failures.
2. Implement tokens, shell, desktop/mobile navigation and shared accessible controls.
3. Redesign discovery, filters, rows and the preview request lifecycle.
4. Redesign problem detail, workflow, solutions, outcomes, voting and evolution.
5. Redesign all contribution, onboarding and settings flows.
6. Finish search, community, moderation, documentation and message pages.
7. Inspect desktop/mobile screenshots, test keyboard and responsive interactions, fix regressions and update design documentation.

## Verification and acceptance

- Styles and scripts return successful responses with correct content types; no critical browser console errors.
- Check all public page families and authenticated/permission-dependent states with existing local test fixtures. Do not bypass authentication for live browser checks.
- Verify filters, preview selection, rapid switching, failed requests, normal mobile navigation, pagination, disclosure expansion, form validation, duplicate suggestions and return navigation.
- Verify valid states for empty lists, no solutions, long content, anonymous authors, invalid/solved problems and users without write permissions.
- Verify light/dark contrast, keyboard focus, screen-reader labels, reduced motion and touch targets; no page-wide horizontal overflow at the target widths.
- Run appropriate interaction regression tests plus the repository's Go/static/generated-code checks. Record environment-dependent failures explicitly; never report unrun checks as passing.
- Preserve all business rules and unrelated local changes. Implementation is complete only when all page families are addressed and material verification gaps are reported.

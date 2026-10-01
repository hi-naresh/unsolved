# Unsolved — Process Atlas design system

The responsive redesign uses a shared server-rendered shell, a desktop explorer with an interactive preview, and mobile navigation with full-page problem reading. The memorable element is the actual process: numbered steps lead into proposals and reported outcomes.

## Current implementation (October 2026)

- Palette: mist `#F3F6F8`, white `#FFFFFF`, navy ink `#172B3A`, steel `#526675`, ocean accent `#006D77`, aqua selection `#E1F2F2`. Dark mode uses blue-gray surfaces and a pale aqua accent. The appearance control cycles system, light and dark and remembers the choice locally.
- Typography: Avenir Next/Avenir with system fallbacks. Reading text is 16px; controls and supporting text have distinct sizes. Headings use a compact measure and sentence case.
- `static/css/input.css` owns theme variables and imports; `primitives.css` owns shared baseline controls; `forms.css` and `pages.css` own existing specialized components; `atlas.css` owns the responsive design, imported last among component sheets.
- Desktop uses a list/detail explorer at 1024px and above. Phones and tablets use full-page links, a labeled bottom navigation bar and one document flow. The shell reserves safe-area space for navigation.
- Preview requests cancel their predecessors and check sequence before updating. Loading and retry states are explicit. Votes display server-confirmed counts; failed requests receive visible feedback.
- `theme.js` resolves appearance before paint; `app.js` enhances navigation, previews and disclosures; `forms.js` provides contribution progress and live previews. Regular links and form posts remain supported.
- Fields have visible focus, disclosures expose expanded state, primary controls have 44px minimum height, and motion respects reduced-motion preferences. Form previews remain operable at every breakpoint.
- Version CSS/JS URLs when shipping changed assets, since static responses are cached. Generated CSS is built with `make css`; generated templ Go files are committed alongside source changes.

Principles: **short, scannable, visual.** A visitor should grasp a problem in five seconds: one-line title, one-line pain, a step flow, and how close it is to solved. Long text is always clamped behind "Show more". Structure (steps, outcomes, revisions, solutions) is drawn, not written.

## Tokens (static/css/input.css)
- Neutrals: use only the `stone-*` ramp and `white`. They are remapped in dark mode, so a page written with `bg-white text-stone-900 border-stone-200` is automatically dark-mode correct. Never use `gray-*`, `slate-*`, `black`, or raw hex for neutrals.
- Accent: `accent`, `accent-soft`, `accent-ink` (ocean). Primary actions, focus rings, current/selected state.
- Semantic hues (fine in both themes because they're used as translucent tints): votes = amber, solved = emerald, open = sky, invalid/errors = red. Use tints like `bg-emerald-500/12 text-emerald-700`, not `bg-emerald-50`.
- Solution kinds each own one hue via `.kind-<kind>` (sets `--k`): process_change sky, off_the_shelf violet, custom_software amber, dont_automate emerald. Use `.kind-chip`, `.kind-dot`, `.kind-rail`, or `var(--k)` in SVG.

## Components (classes)
`card`, `card-hover`, `kicker` (sentence-case supporting label), `muted`, `field`, `btn` (primary, accent), `btn-secondary`, `btn-ghost`, `error`, `chip` (+ `chip-accent|open|solved|invalid|pinned`), `seg` (segmented tabs; mark the active `<a aria-current="page">`), `clamp-2|3|5` + `data-clamp` with a `[data-toggle=<id>]` button (static/js/app.js toggles `.open` and hides the button when nothing is clamped), `flow` (numbered step timeline; `<li class="flow-hidden">` for collapsed steps, toggled via `data-toggle` on the `<ol>` id). Use `length-meter` for form character guidance and `meter` only for search relevance.

Page-specific CSS goes in `static/css/forms.css` or `static/css/pages.css`, never in input.css.

## Layout
- `layouts.Base(title)`: reading/form column (880px outer maximum). `layouts.BaseWide(title)`: workspace (1440px outer maximum).
- Type scale: page title `text-2xl sm:text-3xl font-semibold tracking-tight`; section title `text-base font-semibold`; body `text-[15px] leading-relaxed text-stone-700`; meta `text-xs text-stone-500`.
- Radius: primary surfaces 18–20px, controls 10px, chips rounded. Spacing rhythm 8px.
- Icons: inline SVG, `size-4`, `stroke="currentColor" stroke-width="1.8"`, `aria-hidden="true"`.

## Interaction
- htmx for inline form actions; every action still works as a plain form POST. The read-only explorer preview uses cancellable fetch requests and initializes returned htmx controls.
- Empty states always say what to do next, with a button.
- Forms: one question per block, helper text under the label, live length guidance with `data-aim="<chars>"` plus `<span id="<field-id>-count">` (app.js fills it), errors inline under the field.
- Accessibility: visible focus (global `:focus-visible`), `aria-current`, `aria-pressed` on toggles, labels on every input, SVG graphs have `<title>` and a text alternative.

## Rules that still apply (docs/BUILD.md)
Templates never receive author ids — authorship only via `views.Author`. No new routes, dependencies, tables, columns or config keys. No JS frameworks; tiny vanilla JS only in static/js/app.js. Pages stay within 3 queries.

# Unsolved — UI design system

Principles: **short, scannable, visual.** A visitor should grasp a problem in five seconds: one-line title, one-line pain, a step flow, and how close it is to solved. Long text is always clamped behind "Show more". Structure (steps, outcomes, revisions, solutions) is drawn, not written.

## Tokens (static/css/input.css)
- Neutrals: use only the `stone-*` ramp and `white`. They are remapped in dark mode, so a page written with `bg-white text-stone-900 border-stone-200` is automatically dark-mode correct. Never use `gray-*`, `slate-*`, `black`, or raw hex for neutrals.
- Accent: `accent`, `accent-soft`, `accent-ink` (indigo). Primary actions, focus rings, current/selected state.
- Semantic hues (fine in both themes because they're used as translucent tints): votes = amber, solved = emerald, open = sky, invalid/errors = red. Use tints like `bg-emerald-500/12 text-emerald-700`, not `bg-emerald-50`.
- Solution kinds each own one hue via `.kind-<kind>` (sets `--k`): process_change sky, off_the_shelf violet, custom_software amber, dont_automate emerald. Use `.kind-chip`, `.kind-dot`, `.kind-rail`, or `var(--k)` in SVG.

## Components (classes)
`card`, `card-hover`, `kicker` (small uppercase label), `muted`, `field`, `btn` (primary, accent), `btn-secondary`, `btn-ghost`, `error`, `chip` (+ `chip-accent|open|solved|invalid|pinned`), `seg` (segmented tabs; mark the active `<a aria-current="page">`), `clamp-2|3|5` + `data-clamp` with a `[data-toggle=<id>]` button (static/js/app.js toggles `.open` and hides the button when nothing is clamped), `flow` (numbered step timeline; `<li class="flow-hidden">` for collapsed steps, toggled via `data-toggle` on the `<ol>` id).

Page-specific CSS goes in `static/css/forms.css` or `static/css/pages.css`, never in input.css.

## Layout
- `layouts.Base(title)`: narrow reading/form column (max-w-3xl). `layouts.BaseWide(title)`: split panes (max-w-7xl).
- Type scale: page title `text-2xl sm:text-3xl font-semibold tracking-tight`; section title `text-base font-semibold`; body `text-[15px] leading-relaxed text-stone-700`; meta `text-xs text-stone-500`.
- Radius: cards `rounded-xl`, controls `rounded-lg`, chips `rounded-full`. Spacing rhythm 4/6/8.
- Icons: inline SVG, `size-4`, `stroke="currentColor" stroke-width="1.8"`, `aria-hidden="true"`.

## Interaction
- htmx for every in-page action; every action still works as a plain form POST.
- Empty states always say what to do next, with a button.
- Forms: one question per block, helper text under the label, live length guidance with `data-aim="<chars>"` plus `<span id="<field-id>-count">` (app.js fills it), errors inline under the field.
- Accessibility: visible focus (global `:focus-visible`), `aria-current`, `aria-pressed` on toggles, labels on every input, SVG graphs have `<title>` and a text alternative.

## Rules that still apply (docs/BUILD.md)
Templates never receive author ids — authorship only via `views.Author`. No new routes, dependencies, tables, columns or config keys. No JS frameworks; tiny vanilla JS only in static/js/app.js. Pages stay within 3 queries.

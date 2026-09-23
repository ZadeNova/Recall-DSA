# Recall-DSA — Design System

Reverse-engineered from the shipped frontend. **Source of truth for visual/UI work**: if a value isn't here, look for an existing one before adding a new one, and update this file in the same change as any CSS edit.

- All CSS lives in one inline `<style>` block in `internal/httpapi/templates/layout.html`. There is no other stylesheet, framework, or build step.
- Component CSS references **semantic tokens only** — never raw `--color-*` values or hex codes (exceptions are listed under Colors).
- Product/UX decisions and rationale live in `FRONTEND.md`; this file records what the CSS actually is.

---

## 1. Themes

Three themes, switched by `data-theme` on `<html>`. Light is the default (no attribute match needed; `:root` holds it). The switcher (`.theme-switcher` in nav) sets the attribute live via JS and persists a cookie via `GET /theme/{light|dark|nord}`; the server renders the saved value into `<html data-theme>` on load.

| Theme | `data-theme` | Palette |
|---|---|---|
| Light (default) | `light` (rules on `:root`) | Rose Pine Dawn |
| Dark | `dark` | Rose Pine Moon |
| Nord | `nord` | Nord, mapped onto Rose Pine's role names |

Adding a theme = one new `[data-theme="x"]` block that redefines the raw palette below. No component CSS changes.

## 2. Colors

### 2.1 Raw palette (`--color-*`, swapped per theme)

| Token | Light | Dark | Nord |
|---|---|---|---|
| `--color-base` | `#faf4ed` | `#232136` | `#2e3440` |
| `--color-surface` | `#fffaf3` | `#2a273f` | `#3b4252` |
| `--color-overlay` | `#f2e9e1` | `#393552` | `#434c5e` |
| `--color-muted` | `#9893a5` | `#6e6a86` | `#4c566a` |
| `--color-subtle` | `#797593` | `#908caa` | `#d8dee9` |
| `--color-text` | `#464261` | `#e0def4` | `#eceff4` |
| `--color-love` (red) | `#b4637a` | `#eb6f92` | `#bf616a` |
| `--color-gold` (amber) | `#ea9d34` | `#f6c177` | `#ebcb8b` |
| `--color-rose` (orange/pink) | `#d7827e` | `#ea9a97` | `#d08770` |
| `--color-pine` (blue) | `#286983` | `#3e8fb0` | `#5e81ac` |
| `--color-foam` (teal) | `#56949f` | `#9ccfd8` | `#88c0d0` |
| `--color-iris` (purple) | `#907aa9` | `#c4a7e7` | `#b48ead` |
| `--color-highlight-low` | `#f4ede8` | `#2a283e` | `#3b4252` |
| `--color-highlight-med` | `#dfdad9` | `#44415a` | `#4c566a` |
| `--color-highlight-high` | `#cecacd` | `#56526e` | `#81a1c1` |

`--color-overlay`, `--color-muted`, `--color-highlight-high` are defined but **not referenced by any semantic token or component** today.

### 2.2 Semantic tokens (defined once on `:root`, never per-theme)

| Token | → Raw | Used for |
|---|---|---|
| `--bg` | `base` | Page background; active theme-switcher tab |
| `--surface-bg` | `surface` | Cards, inputs, code blocks, hover fill on nav/secondary buttons |
| `--text` | `text` | Body text, secondary-button text |
| `--muted-text` | `subtle` | `.meta`, subtitles, hints, non-selected switcher tabs |
| `--border` | `highlight-med` | All 1px borders/dividers, bar track |
| `--link` | `pine` | Links, primary buttons, brand, "due today" status, focus ring, notice accent |
| `--danger` | `love` | Errors, destructive button, overdue status |
| `--tag-bg` | `highlight-low` | Topic tags/chips, due pill, active nav link |
| `--tag-accent` | `iris` | Topic tag/chip text, checked chip fill, bar fill |
| `--grade-failed` / `-hard` / `-good` / `-easy` | `love` / `gold` / `pine` / `foam` | Grade buttons and grade cards |
| `--difficulty-easy` / `-medium` / `-hard` | `foam` / `gold` / `rose` | Difficulty text and pills |

### 2.3 Color rules in use

- **On-fill text** (text sitting on a solid `--link`, `--danger`, grade, difficulty, or `--tag-accent` fill) is `var(--color-base)` — the one place component CSS reads a raw token.
- **Grade and difficulty colors are the "state language"**: a solid fill of that color = selected/active (grade buttons, grade cards, difficulty pills, checked topic chips).
- **Muted** = `--muted-text` on `--surface-bg`; used for de-emphasis, not disabled state.
- Opacity is used only for the htmx swap fade (`.grade-cell.htmx-swapping` → `0`). No alpha colors or shadows anywhere.

## 3. Typography

Fonts (self-hosted woff2, `font-display: swap`):

- `--font-sans`: `"IBM Plex Sans", system-ui, sans-serif` — variable, weights 400–600
- `--font-mono`: `"IBM Plex Mono", ui-monospace, monospace` — weight 400 only

Mono (`code`, `.mono`, `.import-textarea`, `.code-block`) is for code, URLs, slugs, and raw system/error text (`error.html` message). Never for authored prose.

Base: `body` 15px / 400 / line-height 1.5. Inputs, selects, textareas, buttons inherit family and are 15px.

| Role | Selector | Size / weight |
|---|---|---|
| Page title | `h1` | 28 / 600, lh 1.25 |
| Section heading | `h2` | 20 / 600, lh 1.25 |
| Stat number | `.stat-number` | 20 / 600 |
| Brand | `.brand` | 17 / 700, ls −0.01em |
| Section title (forms) | `.section-title` | 16 / 600 |
| Problem title link | `.problem-title` | 16 / 500 |
| Body / inputs / buttons | `body`, `button` | 15 / 400 · buttons 500 |
| Difficulty pill label | `.difficulty-pill` | 15 / 600 |
| Notice | `.notice` | 14 / 400 |
| Meta, subtitles, due pill, status, grade buttons, switcher | `.meta`, `.section-subtitle`, `.due-pill`, `.status-*`, `.grade-btn`, `.theme-switcher a` | 13 / 400–500 |
| Table headers, labels, hints, tags, chips, difficulty text | `th`, `label`, `.field-hint`, `.topic-tag`, `.topic-chip`, `.difficulty-*` | 12 / 500 |
| Import textarea | `.import-textarea` | mono 13 |
| Code block | `.code-block` | mono 12 |
| Grade card name | `.grade-card-name` | 11 / 700, uppercase, ls 0.04em |
| Grade card interval | `.grade-card-interval` | 22 / 700 |
| Grade card desc | `.grade-card-desc` | 12 / 400 |

Sizes used outside FRONTEND.md's stated scale (28/20/16/15/13/12): 22, 20 (stat), 17, 14, 11 and weight 700. They are all present in the code above; treat them as part of the system until deliberately consolidated.

## 4. Spacing & layout

No formal scale — ad hoc `rem`. Recurring values:

- **Page**: `body` `max-width: 1200px; margin: 2rem auto; padding: 0 1rem`. Viewport meta present; desktop-first (mobile is not a target).
- **Gaps**: `0.25rem` (tight), `0.5rem` (default inline gap), `0.75rem` (card/stat grids, action rows), `1rem`, `1.5rem` (major blocks, field rows).
- **Block padding**: cards `0.75rem 1rem`; stat card `0.6rem 0.9rem`; buttons `0.4rem 0.9rem`; grade buttons `0.25rem 0.55rem`; table cells `0.4rem 0.6rem`; inputs `0.35rem 0.5rem`; nav links `0.35rem 0.7rem`; tags `0.1rem 0.5rem`; chips `0.15rem 0.55rem`; pills (due) `0.2rem 0.6rem`; difficulty pill `0.65rem 0.6rem`; grade card `0.9rem 1rem`.
- **Vertical rhythm**: `h1` margin `0 0 0.5rem`; `h2` `1.5rem 0 0.5rem`; `.section-title` `1.5rem 0 0.25rem`; stat rows/toolbars `margin: 1rem 0`; `.form-actions` `margin-top: 1.5rem`.
- **Layout primitives** (all flex, all `flex-wrap: wrap` where multi-item):
  - `.stats-row` — gap 0.75rem; children `.stat-card` `flex: 1; min-width: 180px`
  - `.home-layout` — gap 1.5rem; `.home-main` `flex: 2; min-width: 320px`, `.home-sidebar` `flex: 1; min-width: 240px`, column, gap 0.75rem
  - `.field-row` — gap 1.5rem; children `flex: 1; min-width: 220px`
  - `.library-toolbar` — gap 0.5rem, centered; text input `flex: 1; min-width: 200px`
  - `.form-actions` — gap 0.75rem, centered
  - `.pagination` — space-between; `.pagination-controls` gap 0.75rem
  - `nav` — space-between, gap 0.5rem, bottom border
- **Tables**: always wrapped in `.table-scroll` (`overflow-x: auto; margin-top: 1rem`); `th`/`td` left-aligned, `vertical-align: top`, 1px bottom border only (no vertical rules, no zebra).

## 5. Radii, borders, surfaces

- **Radii**: `4px` (default: buttons, inputs, cards, tags, notice, code block, topic-chip container) · `6px` (segmented theme switcher, difficulty pills, grade cards) · `999px` (due pill, topic chips) · `3px` (progress bar).
- **Borders**: always `1px solid var(--border)`. Exceptions: `.notice` has a 3px `--link` left border; selected pills/cards swap their border to the fill color.
- **Surfaces**: two levels only — page `--bg` and raised `--surface-bg`. Raised surfaces (stat/sidebar/section cards, muted section, notice) are **borderless** fills; interactive/form surfaces (inputs, code block, topic-chip box, pills, grade cards) are bordered. No box-shadows.
- **Primary vs. muted cards**: `.section-card` (primary, actionable) and `.muted-section` (de-emphasized: 13px `--muted-text`) share the same fill and radius.

## 6. Components

**Nav** — `nav` bar: `.brand` (accent, bold, links home) + `.nav-links` pills; right side has `.due-pill` and `.theme-switcher`. Current page gets `.nav-active` (`--tag-bg` fill, `--link` text). Links are `--text`, weight 500, no underline; hover fills `--surface-bg`.

**Due pill** (`.due-pill`) — rounded-full, `--tag-bg` fill, `--link` text, 13/500. Also used inline in Library's `<h1>`.

**Theme switcher** — bordered segmented control (`6px` radius, 2px padding, `--surface-bg`); tabs `--muted-text`; active tab `--bg` fill, `--text`, 600.

**Buttons**
- Primary (`button`, `a.btn-link`): `--link` fill, `--color-base` text, no border, 4px radius, 500.
- Danger (`button.btn-danger`): `--danger` fill.
- Secondary (`a.btn-secondary`, `button.btn-secondary`): transparent, `--text`, 1px `--border`; hover `--surface-bg` fill (no brightness change).
- Grade (`.grade-btn-failed|hard|good|easy`): small solid fill of the matching `--grade-*` token; each is its own inline `<form class="grade-form">`.
- Every plain `<button>` gets primary styling with no class; more specific classes override.

**Form controls** — `input[type=text]`, `input[type=number]`, `select`: 1px `--border`, 4px radius, `--surface-bg` fill, `--text`. Native `select`/`checkbox`/`radio` behavior otherwise. `.input-small` = `6rem` wide. `.import-textarea` = mono 13, vertical resize. Field label row: `.field-header` (label left, `.field-hint` right, baseline-aligned).

**Bulk actions (Library)** — `.bulk-bar` (flex, wrap, gaps `0.5rem 0.75rem`, `margin-top: 1rem`) above the table: a `.meta` "N selected" count, a primary **Pause selected** button, and `.bulk-unpause` (inline flex) holding the secondary **Unpause selected** button, a 12/500 `--muted-text` label and an `.input-small` number input. The table's first column is `th.check`/`td.check` (`2.2rem`, no right padding) holding the row and select-all checkboxes. `.badge-paused` is a `.topic-tag`-shaped badge (4px radius, `--tag-bg` fill, 12/500) in `--muted-text`. Paused/selected row states are in §7.

**Cards** — `.stat-card` (meta label + `.stat-number`), `.sidebar-card`, `.section-card`, `.muted-section`, `.quote-card` (italic, muted). See §5.

**Tags & chips**
- `.topic-tag` (read-only): 4px radius, `--tag-bg` fill, `--tag-accent` text, 12/500.
- `.topic-chip` (selectable, in `.topic-chips` scroll box `max-height: 220px`): pill-shaped, same colors; checked → solid `--tag-accent` fill with `--color-base` text. Hidden by the filter via `[hidden] { display: none }` (required because chip sets its own `display`).

**Selectable option controls** (hidden radio/checkbox inside a `<label>`, state via `:has(input:checked)`)
- `.difficulty-pill` (Easy/Medium/Hard): bordered, equal-width; checked → solid `--difficulty-*` fill and border, `--color-base` text.
- `.grade-card` (Failed/Hard/Good/Easy): name / interval / description; checked → solid `--grade-*` fill and border, all inner text `--color-base`.

**Difficulty & status text** — `.difficulty-Easy|Medium|Hard` color the text 12/500. `.status-paused` (`--muted-text`, 13/500) is the Next Review text for a paused problem. `.status-overdue` (`--danger`, 500), `.status-due-today` (`--link`, 500), `.status-upcoming` (`--muted-text`). Import row status: `.import-status-new|merge|protected|error` (text / muted / link / danger).

**Progress bars** — `.topic-count-bar` (6px tall, 3px radius, `--border` track, min-width 80px) with `.topic-count-bar-fill` (`--tag-accent`); width set inline as a Go-computed percentage.

**Lists** — `.upcoming-by-day`: unstyled list, flex space-between rows, 1px bottom border except last.

**Pagination** — "Showing X–Y of N" (`.meta`) left; Prev/Next as `.btn-secondary` links and a page-size `<select>` (auto-submits; `<noscript>` Apply button) right.

**Messages** — `.error` (`--danger` text), `.notice` (surface fill, 3px `--link` left border, 14px), page header trio: `h1` → `.page-subtitle` (muted) → `hr.page-divider`.

**Code block** — `.code-block`: surface fill, 1px border, 4px radius, mono 12, `white-space: pre`, horizontal scroll.

**Links** — plain `a` is `--link` with default underline. Nav, buttons, pills, and switcher tabs remove underline. Outbound problem links use `target="leetcode"`.

## 7. Interaction states

| State | Treatment |
|---|---|
| Hover (filled buttons, `.btn-link`, `.due-pill`) | `filter: brightness(1.1)` |
| Hover (nav links, `.btn-secondary`) | `--surface-bg` background |
| Hover (theme-switcher tab) | `--bg` background |
| Active nav / theme | Nav: `--tag-bg` + `--link`. Switcher: `--bg` + `--text` + 600 |
| Selected (pill/card/chip) | Solid fill of its semantic color, `--color-base` text |
| Selected table row (`.row-selected`) | `--tag-bg` background on its cells (rows are not pills, so no solid fill) |
| Paused table row (`.row-paused`) | `--surface-bg` background, `--muted-text` title, `.badge-paused`; no opacity |
| Keyboard focus, custom controls | `:has(input:focus-visible)` on `.topic-chip`, `.difficulty-pill`, `.grade-card` → `outline: 2px solid var(--link); outline-offset: 2px` |
| Keyboard focus, everything else | Browser default (no custom rule) |
| Pressed / disabled | Not styled (no `:active`, `:disabled`) |
| Destructive confirm | Native `confirm()` on delete forms |
| Grade submitted (htmx) | Row's `.grade-cell` fades out (`opacity 0`, `.htmx-swapping`), swaps to `graded-badge` (`.meta`, "✓ Graded X — next review DATE"); stats, overdue count, and nav pill update via out-of-band swap |
| Filter topics | Non-matching `.topic-chip[hidden]`; Enter in the filter is blocked |
| Empty states | Plain paragraph in place of the table (`Nothing due right now…`) |

**Motion** — everything below is inside `@media (prefers-reduced-motion: no-preference)`:

- Theme change: `background-color`, `color`, `border-color`, `fill` ease over **1s** on `body, body *`
- Hover feedback: `filter` + `background-color` **0.25s** on buttons, `.btn-link`, `.due-pill`, nav links, switcher tabs, `.grade-btn`
- `.grade-cell` opacity **0.2s** (htmx `swap:0.2s settle:0.2s`)
- `.topic-count-bar-fill` `grow-bar` keyframe (from `width: 0`) **0.6s ease-out**, once on load

## 8. Not present

No icons, images, shadows, gradients, animations beyond §7, dark-mode media query (theme is cookie-driven, not `prefers-color-scheme`), breakpoints/media queries for layout (responsiveness comes from `flex-wrap` + `min-width`), or CSS variables for spacing/radius/font-size.

## 9. Rules for future frontend work

1. Use semantic tokens; add a new one only when a new *purpose* appears, mapped onto an existing raw color.
2. Any new theme redefines the raw palette only.
3. Reuse an existing component or size from §3/§6 before adding one; record additions here.
4. Selected/active = solid fill of the relevant semantic color with `--color-base` text.
5. Any element with a custom `display` that is toggled via `hidden` needs its own `[hidden] { display: none }` rule.
6. Give hidden-input custom controls a `:has(input:focus-visible)` outline.
7. New motion goes inside the `prefers-reduced-motion: no-preference` block.
8. Derived display values (percentages, labels, status classes) are computed in Go, not in the template.

# Recall-DSA — Frontend Design & Specification (v1)

This is the living reference for frontend/UI/UX decisions: visual identity (colors, typography), page-by-page specs, decided UX behavior, open questions, and implementation conventions for the Go `html/template` + htmx frontend. SPEC.md and CLAUDE.md own the backend/product spec; this file owns how it looks, feels, and is built on the frontend.

**Read this before making any visual or UX change**, the same way SPEC.md is read before a backend/architecture change. When a decision gets made in discussion, it belongs here — not just in chat history.

## Stack (from CLAUDE.md)

- Server-rendered HTML via Go's `html/template` — no client-side framework
- htmx for interactivity — attributes only, no build pipeline, no npm, no React/Vue, no bundler
- htmx embedded via `go:embed` — no CDN dependency
- CSS: currently plain inline `<style>` in the shared layout template; no CSS framework or preprocessor (rules out Tailwind/Sass/etc. unless this doc explicitly revisits that)
- One Go binary serves both API and HTML

## Current build status

- **Step 4 (HTTP handlers)** — complete: plain-HTML, no-JS forms/pages covering the full workflow (dashboard, due queue, grading, add/edit/delete problems, library, topics).
- **Step 5 (htmx templates/UI)** — not started. A UX walkthrough of the daily workflow has been done; the landing-page/dashboard decision from that walkthrough is already built (see below). Three friction points from that walkthrough remain open (see "Open UX questions").

## Visual identity — TBD

Nothing below has been deliberately chosen yet. Don't let implementation drift ahead of what's actually decided here — if a color or type choice shows up in a template, it should trace back to an entry in this section.

- **Colors**: TBD. Current implementation uses ad hoc values with no palette (`#222` body text, `#eee` topic tags, `#b00020` error red, `#ddd` borders).
- **Typography**: TBD. Currently `system-ui, sans-serif`, no deliberate type scale (no defined heading sizes, line-height system, etc.).
- **Spacing/layout scale**: TBD. Currently ad hoc rem values (0.4rem, 2rem, ...), no systematic scale.
- **Iconography**: none currently; TBD whether any is wanted.

**Deferral strategy (decided):** rather than either (a) blocking further page/structure work on picking final values, or (b) continuing to hardcode ad hoc colors per page, new work should express color/spacing as **semantic CSS custom properties** (e.g. `--bg`, `--text`, `--muted-bg`, `--muted-text`, `--accent`, `--danger`, `--border`) defined once in `layout.html`, with reasonable placeholder values. Structural decisions that depend on "look" (e.g. a muted section) reference the token, not a literal color. When real values are chosen later, it's a one-place edit, not a re-implementation.

## Decided UX facts

1. **Home (`/`) is a pure-glance dashboard, not an action surface.** It shows a due count and a compact, read-only due list (title/topics/next-review-date — no grade buttons, no Upcoming section). Every row's title still links out to LeetCode as normal. Clicking through (a "Go to Due →" link) is the only path from Home into actually grading anything. Rationale: avoids two places that can both mutate review state, and matches the mental model of "Home = orientation, `/due` = where you act" — decided explicitly over the alternative (keeping Home's table gradable) after weighing both.
2. **`/due` is now the single place that shows both Due and Upcoming, topic-filterable together.** One topic dropdown drives both sections — selecting "Graphs" filters Due-Graphs and Upcoming-Graphs at once. This requires `RecommendUpcoming` to accept the same topic filter `RecommendDue` does (currently it doesn't — service-layer change pending).
3. **Upcoming items are view-only** (no grade buttons) — grading before a problem is actually due would inflate ease/interval on a false signal, undermining genuine retention testing. This is a UI-level choice only; the backend (`RecordReview`) does not enforce a due-date check.
4. **Upcoming lookahead window is 7 days.**
5. **On `/due`, Upcoming is structurally distinct from Due**: both sections visible on one page (no tabs, no side-by-side columns — stacked, Due on top), Upcoming rendered with a muted/de-emphasized treatment (via the placeholder `--muted-bg`/`--muted-text` tokens above) and no action buttons.
6. The persistent top nav (Due / Library / Add Problem / Topics) is treated as sufficient "quick links" — Home does not duplicate them as separate cards.
7. Every outbound LeetCode link uses **named-window targeting** (`target="leetcode"`), so clicking through always reuses one browser tab (SPEC.md §7) — never omit this on a page that links to a problem.
8. Destructive actions (delete problem, delete topic) use a native `confirm()` guard — the only JS in the app so far, pending htmx.
9. **One shared error-page template covers 400/404/500** (routed through the existing `renderError` helper, plus replacing the raw `http.NotFound` call in the edit-problem handler) — same layout, status-specific heading. Shows the actual underlying error text (e.g. `service: invalid difficulty "Extreme"`), not a generic "something went wrong" message — reasonable since this is a single-user, self-hosted tool (SPEC.md §10) where the user is also the one who'd debug it. 422s (form validation failures) are unaffected — those already re-render the actual form with an inline error so input isn't lost, which is better UX than a separate error page.

## Open UX questions — not yet decided, do not build ahead of these

1. **Grading interaction feel** — currently four full-page-reload forms per due row, no feedback on what a grade just did (the row just vanishes). Candidate direction: an htmx inline swap so grading a batch feels continuous, plus visible confirmation (e.g. "next review in 13 days").
2. **Topic input UX** — currently a free-text comma-separated field on add/edit forms, no visibility into existing topics, risking near-duplicate tags (e.g. "Array" vs "Arrays & Hashing" — the schema's case-insensitive uniqueness doesn't catch typos). Candidate direction: a multi-select/checkbox or autocomplete picker fed by `ListTopics`.
3. **Due ↔ Library connection** — no way to jump from "I'm reviewing Graphs today" into "let me also skim my Graphs library" without re-navigating and re-filtering from scratch.

## Page inventory

| Page | Route(s) | Purpose | Status |
|---|---|---|---|
| Home / Dashboard | `GET /` | Due count + read-only glance list, links to /due | Built (plain HTML, pure glance) |
| Due | `GET /due` | Due (gradable) + Upcoming (view-only), one shared topic filter | Built (plain HTML) |
| Add Problem | `GET /problems/new`, `POST /problems` | Create + first-grade a problem in one action | Built (plain HTML) |
| Edit Problem | `GET /problems/{id}/edit`, `POST .../update`, `POST .../delete` | Edit or delete a problem | Built (plain HTML) |
| Library | `GET /library` | Browse all logged problems, filter by topic/difficulty | Built (plain HTML) |
| Topics | `GET /topics` + create/rename/delete | Topic CRUD | Built (plain HTML) |
| Error page | (shared, not a route) | 400/404/500 — status, underlying error text, link home | Built (plain HTML) |
| Bulk Import | — | CSV/paste seeding, baseline Hard grade, staggered dates (SPEC.md §9) | Not built — deliberately deferred to CLAUDE.md build-order step 6 |

## Frontend implementation conventions

Parallel to CLAUDE.md's "Go Guidelines" — these govern template/HTML/CSS/htmx work specifically.

- Templates live in `internal/httpapi/templates/`: one file per page, plus `layout.html` (shared chrome) and `partials/` (reusable fragments, e.g. `due-table.html`).
- Each page is parsed as its own isolated `*template.Template` (`layout.html` + all partials + that one page file) — **never** parse all page files together. `{{define "content"}}` (and any other shared block name) collides across pages within a single template set; see the doc comment on `pageTemplates` in `internal/httpapi/templates.go`.
- No inline `<script>` beyond the `onsubmit="return confirm(...)"` guards on destructive actions, until htmx is deliberately introduced as the next step.
- No CSS framework, no build step — hand-written CSS, currently in `layout.html`'s `<style>` block. Revisit only if this doc's visual-identity section explicitly decides otherwise.
- Keep forms working without JS wherever possible — htmx should be an *enhancement* layer, not a hard requirement for basic functionality, mirroring SPEC.md's "never a hard dependency" philosophy elsewhere (e.g. §9's optional LeetCode metadata lookup).
- When asked to make a frontend/visual change, check this file's "Visual identity" and "Decided UX facts" sections first — don't invent a color, font, or UX behavior that contradicts (or should have been recorded in) this doc.

## How to use this file

- Before implementing any frontend/UI change: read this file, the same way SPEC.md gets read before a backend change.
- When a UX or visual decision is made in discussion: record it under "Decided UX facts" (or fill in "Visual identity" once colors/type are actually chosen).
- When a new open question surfaces: add it under "Open UX questions" so it isn't lost to chat history.

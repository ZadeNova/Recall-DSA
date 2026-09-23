# New Features — Design Notes

Features discussed after v1 was built, tracked here rather than in SPEC.md to keep the core spec lean. SPEC.md §14 points here. Read SPEC.md (especially §3–§5, §9, §13) before implementing anything below.

**Why these exist:** a mass import (206+ problems and growing) showed that nothing ever leaves the review pool. `RecommendDue` has no concept of "done with this forever", so total daily load only grows as more problems are solved. The 45-day cap bounds a single problem's *interval*; nothing bounds the *pool*.

**Status at a glance**

| Feature | Status |
|---|---|
| Pause/archive (bulk, via Library) | **Built** on `feature/pause-unpause`; pending final check against real data and merge |
| Unpause behavior (staggered) | **Built** with pause |
| Collections (NC150, Blind 75, ...) | Design settled — build **after** pause/unpause ships and is reviewed |
| Stuck/leech signal | Reserved for later |
| Daily review cap with rollover | Deferred |
| Settings page | Idea only |
| Interval jitter | Dropped |

---

## 1. Pause/archive a problem out of active rotation — built

Removes a problem from the review pool while keeping its `attempts` history intact. Not gated on grade history: valid for "clearly mastered, doesn't need indefinite review" or "not worth reviewing forever regardless of mastery" (e.g. an obscure pattern unlikely to come up in an interview). Also the resolution path for the stuck/leech signal (§4). Which problems qualify is always a manual judgment call. The tool inferring it would be the same automation SPEC.md §13 rejects for curriculum guidance, aimed at curation instead of recommendation.

**Intended use:** keep NC150 (plus chosen complementary problems) active, and bulk-pause the extras (~56 of the current 206). This is cheaper than pausing everything and then unpausing ~150, and avoids a mass unpause landing at once.

### Schema
- Add `paused_at TIMESTAMP NULL` to `review_state`. `NULL` = active; the timestamp also gives "paused N days ago" for free.
- Lives on `review_state`, not `problems`: it gates what `RecommendDue` returns, and `review_state` is already "live scheduler state, mutated in place" (SPEC.md §3).
- **Migration caveat:** first schema change since v1 that isn't a fresh `CREATE TABLE`. `CREATE TABLE IF NOT EXISTS` is a no-op on an existing table, so this needs a one-time, idempotency-guarded `ALTER TABLE review_state ADD COLUMN paused_at ...` (check `PRAGMA table_info` first) run at startup.

### Service
- New bulk methods `PauseProblems(ctx, ids)` / `UnpauseProblems(ctx, ids)`, each one transaction with a single `UPDATE ... WHERE problem_id IN (...)`.
- **Pause is idempotent:** `SET paused_at = now WHERE ... AND paused_at IS NULL`, so re-pausing keeps the original timestamp, and the rows-affected count ("Paused N problems") only counts problems that actually changed.
- **Unpausing an already-active problem does nothing:** the unpause update must be restricted to `paused_at IS NOT NULL`. Otherwise an active row in a mixed selection would have its next review silently pushed back.
- **Every query that shows or counts due work must exclude paused problems.** Missing one makes counts disagree with lists (the same class of bug as FRONTEND.md #23):
  - `RecommendDue`, `RecommendUpcoming`
  - `CountDue` (nav "N due today" pill)
  - `DueStats` (Due page stats row + htmx OOB refresh after grading)
  - `UpcomingByDay` (Home's Review Load sidebar)
  
  Define the "active" predicate once and reuse it rather than hand-repeating it.
- `ListLibrary` gains a status filter (All / Active / Paused).
- New count for the **Paused: N** stat.

### Handlers
- One bulk endpoint, e.g. `POST /problems/bulk-status`, taking the selected ids plus an `action` of `pause` or `unpause`.
- Post/redirect/get: redirect back to `/library` with the current filters/page preserved, and show a short confirmation ("Paused 10 problems").

### Frontend (Library only)
- **Placement:** Library only, not Due. Library already owns problem-lifecycle actions (edit/delete); Due stays grade-or-view only.
- **Multi-select:** the table becomes a `<form method="post">` with a checkbox per row, a "select all on this page" checkbox, and **Pause selected** / **Unpause selected** buttons. Separate from the existing GET filter form (forms can't nest). Works without JS.
- **No per-row pause button:** ticking one row covers the single-problem case.
- **Selection is per page:** no cross-page selection memory (that needs fragile JS state). At page size 50, 206 problems is 5 pages.
- **One table with a status filter** (All / Active / Paused, default **Active**) next to the existing topic/difficulty/sort filters. Two separately-paginated tables was considered and rejected: an extra query and render on every visit for what's meant to be an occasional view.
- **After a bulk action the page reloads (option B, decided).** Under the default Active filter, freshly paused rows disappear, which is what a filter should do. They show under All/Paused. This replaces the earlier "rows never disappear on pause" decision: the in-place htmx swap that would have kept that promise was judged more complex and bug-prone than it's worth.
- **Paused rows under All:** muted styling plus a "Paused" badge, and the Next Review column shows "Paused since <date>" rather than a stale overdue date. (UX details are recorded in FRONTEND.md #33.)
- **Stats:** Library and Home gain a **Paused: N** count. "Total Tracked" is unaffected (solve history, not rotation membership).

### Verified non-collisions
- Bulk import's "protect real progress" rule (SPEC.md §9) keys off attempt count, not pause state, so re-importing an old CSV against a paused problem still leaves it untouched.

### Edge cases (from code review)
Items marked **confirm** need a decision before building; the rest are the planned handling.
- **Grading a paused problem through another path — decided: stays paused.** Add Problem with an existing slug never creates a duplicate (`findProblemIDBySlug`); it records a new grade on the existing problem (SPEC.md §9). A stale Due tab can also still POST a grade. No grade ever changes pause state; only Pause/Unpause do. The attempt is still logged and `review_state` still updates normally (the history stays honest), and the problem stays out of rotation. Add Problem's success message notes "this problem is paused".
- **Upsert must never reset `paused_at`.** `recordReview`'s `ON CONFLICT ... DO UPDATE SET` list doesn't include it today; add a test so a future edit can't silently unpause problems on every grade.
- **Unpause never pulls a review earlier — decided: whichever date is later.** New `next_review_date` = the later of its existing date and its staggered slot. For long pauses the old date is in the past, so the staggered slot wins (e.g. paused with 6 days left, unpaused 2 months later → back tomorrow). For short pauses the original date is kept, so a review never happens early (grading early inflates ease on a false signal, the same reason `due.go` blocks grading Upcoming items).
- **Stagger details.** The first slot is today + 1 (`1 + i*window/n`, not `i*window/n`, which gives today for i=0). Order problems most-overdue-first so the oldest come back soonest. Reuse import's handling of invalid targets (0, negative, empty).
- **Migration on both paths.** Fresh installs get `paused_at` from `schema.sql`'s `CREATE TABLE`; existing DBs get the guarded `ALTER`. Test both. Run `scripts/backup.sh` on the Pi before the first deploy with the migration.
- **Library sort under All — decided: paused rows sort last**, for every sort option (`ORDER BY (paused_at IS NOT NULL), <sort>, problem_id`). Otherwise paused rows with stale dates mix in, and paused-but-overdue ones float to the top.
- **Topics page wording clash — decided: rename.** Topics' "Active Problems" column counts all problems, including paused; with "active" now meaning "not paused", it's renamed to "Problems".
- **Bulk form input.** No boxes ticked: show a message, not an error. Non-numeric or unknown ids: ignore or 400, never 500. Rebuild the redirect from known filter params rather than echoing a raw URL (avoids an open redirect).
- **The status filter must survive pagination.** `handleLibrary` rebuilds Prev/Next links from an explicit `extra` set of params (q/topic/difficulty/sort). `status` has to be added there, or clicking Next silently drops back to Active. Default Active = param absent; "all"/"paused" are explicit values.
- **Where `paused_at` lives in Go types.** `DueItem` embeds `ReviewState`, which `RecordReview` also returns without reading `paused_at`. Put the field somewhere only populated by queries that actually select it (e.g. on `DueItem`), so a zero value never reads as "active" by accident.
- **Migration hook:** `db.Open` runs `schema.sql` then `seedTopicsIfEmpty`. The guarded `ALTER` slots in right after the schema step, following that same startup-step pattern.
- **One predicate, two query shapes:** `RecommendDue`/`DueStats` alias the table as `rs`, while `CountDue`/`UpcomingByDay` query `review_state` bare. Align them (use the alias everywhere) so a single shared predicate works in all five.
- **Docs after building:** record the new Library/Topics UX in FRONTEND.md's "Decided UX facts" (per CLAUDE.md), and update FRONTEND.md #13's "Active Problems" wording to match the rename.
- **Empty Due page when everything is paused.** Show "N problems paused" next to "nothing due" so an empty queue isn't mistaken for a bug.
- **Already handled by existing code:** pausing the last rows on the last page is covered by Library's existing out-of-range page clamp. Deleting a problem cascades its `review_state`, `paused_at` included.

---

## 2. Unpause behavior — built

- Keep `ease_factor` / `repetitions` / `interval_days` as they were (no lost progress).
- Set `next_review_date` a short way out, not "resume exactly where it left off". Resuming as-is would dump a heavily overdue backlog, and elapsed pause time doesn't map to anything SM-2 models.
- **Bulk unpause is staggered, not a fixed offset.** A fixed +2 days would land every unpaused problem on the same day, recreating the pile-up pause exists to prevent. Spread them with the same offset math bulk import uses (`i * window / n`, window sized from a per-day target), starting at +1 day, **without** import's 14-day minimum window (unpausing 3 problems shouldn't spread them over two weeks).
- **Per-day target — decided:** an inline "Target reviews per day" number input next to the **Unpause selected** button (default 5), matching bulk import's one-time `target_per_day` input. Not a stored setting (SPEC.md §4/§9). A settings page was considered and parked as an idea (§6).

---

## 3. Collections — build after pause/unpause

Named, fixed problem lists (NC150, Blind 75, Grind 75, a personal "Complementary" list). Added because filters can't express "everything not in NC150": the app didn't know which list a problem belongs to, and picking rows by eye doesn't scale (a 4000-problem library makes per-row selection unworkable at any page size).

**Build order:** pause/unpause (§1–§2) ships first, gets reviewed, then collections are built on top as a separate change.

### Core decisions
- **Collections only group and filter. `paused_at` stays the single on/off switch** for whether a problem is reviewed. No "collection is in rotation" flag: two sources of truth would need precedence rules (a problem in an active and an inactive collection, or manually paused).
- **Membership is keyed by slug, not `problem_id`.** It can include problems not yet solved: paste NC150's 150 slugs once, and any of them added later is automatically a member. Joins on `problems.slug` (already the unique identifier, SPEC.md §3).
- **Unordered.** No position/order column. SPEC.md §3 deliberately removed `nc150_order`; collections must not reintroduce it.
- **No recommendations, ever.** Descriptive counts are fine ("NC150: 118 of 150 tracked"). Anything like "next unsolved NC150 problem" or a suggested order is the curriculum guidance SPEC.md §13 rejects.
- **Flexibility beyond NC150 is preserved.** Every problem is tracked and reviewed unless explicitly paused, whatever collections it's in. Keep extra problems active by putting them in a personal collection (e.g. "Complementary") and including it in the bulk pause below, or by unpausing them via Library multi-select. Newly added problems always start active.

### Schema (mirrors topics)
```
collections            id, name UNIQUE COLLATE NOCASE
collection_problems    collection_id, slug      -- PK (collection_id, slug)
```
Deleting a collection deletes its membership rows only, never problems (same rule as topics, SPEC.md §8).

### Features
- **Collections page:** create/rename/delete; set membership by pasting slugs or LeetCode URLs (reuse the import page's slug parsing); per-collection descriptive count of tracked vs total.
- **Library filter:** Collection = X, or **Not in** X.
- **Explicit bulk actions**, each confirmed with a count before running ("This will pause 3,812 problems"):
  - **Pause every tracked problem not in the selected collection(s)**: accepts one or more collections, so "NC150 + Complementary" stay active.
  - **Unpause every problem in a collection**: goes through the staggered unpause (§2).
- Library multi-select (§1) stays for small everyday edits.

### Edge cases (from code review)
- **Editing a problem's URL changes its slug** (`UpdateProblem` rewrites `slug`), which would silently drop it from every collection. Update `collection_problems` slugs in the same transaction.
- **Typos in pasted slugs** can't be checked without calling LeetCode (SPEC.md §13), so they'd sit as phantom "not yet tracked" members forever. List untracked slugs on the collection page so typos are visible and removable.
- **"Pause everything not in the selected collections" with none selected** would pause every problem. Require at least one collection.
- **Large bulk actions (thousands of problems)** must use a subquery (`WHERE problem_id IN (SELECT ...)`), not thousands of bound `?` parameters, which can exceed SQLite's variable limit.
- **The confirmation count must come from the same query as the action,** so "will pause 3,812" matches what actually runs.
- **Deleting a problem keeps its membership** (keyed by slug, so it's still "in NC150"). Intended: it shows as untracked again.

---

## 4. "Stuck" / leech signal — reserved for later

The inverse of pause's mastered-problem case. A problem Failed N times in a row (default N=3) re-enters the queue every day, because Failed always resets `interval_days` to 1 with no limit on repeats. It can quietly take a disproportionate share of daily reviews with no progress.

- **Trigger:** the last N `attempts` rows for a problem are all `Failed`. Distinct from `review_state.repetitions`, which increments on Hard (a pass). Any non-Failed grade breaks the streak.
- **No schema change:** derived from `attempts` at read time (same "derive, don't store" precedent as SPEC.md §3's "solved"). Use one batched query per page of rows (e.g. `ROW_NUMBER() OVER (PARTITION BY problem_id ORDER BY attempted_at DESC)`), following the existing batching idiom (`CheckSlugs`, `attachTopicsToItems`).
- **Surfacing:** passive badge on Due and Library rows. Informational only, never auto-acted on.
- **Resolution:** reuse pause/archive. Pause, relearn outside the SRS loop, unpause later. No second mechanism.
- **Open:** hardcode N=3 or make it configurable. The streak ignores pause gaps: failing right after an unpause re-triggers immediately, which is a real signal.

---

## 5. Daily review cap with rollover — deferred

Show at most N due items per day (most overdue first) with a "show all overdue" escape hatch.

- Largely overlaps with what exists: `RecommendDue` already sorts most-overdue-first and Due is already paginated at 10/page, so page 1 already behaves like a cap of 10. A real cap would mainly change the displayed counts.
- Would amend SPEC.md §4 ("show everything due"; workload is not a stored setting).
- Revisit only if pause alone doesn't bring daily load under control.

---

## 6. Settings page — idea only

A page for user-configurable preferences (e.g. unpause stagger per day, stuck threshold N, a daily cap). Not worth it for one number: it needs a new page, a nav entry and a settings table, and goes against SPEC.md §4's "not a stored setting" stance. Revisit only if several real preferences pile up.

---

## 7. Interval jitter — dropped

Considered (~±10% randomization of `next_review_date` to stop problems re-clustering) and removed:
- Makes the scheduler non-deterministic, breaking its hand-checked trace tests unless seeded from a stable value.
- Compounds if it ever leaks into `interval_days`.
- Can breach the 45-day cap unless clamped afterwards.
- Only prevents future clustering; pause addresses the actual load problem.

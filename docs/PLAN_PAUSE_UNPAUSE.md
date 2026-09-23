# Implementation Plan — Pause/Archive + Unpause

**Status: Steps 1-7 are done on `feature/pause-unpause`. Remaining: the Verification section against a copy of the real database, then merge to `main`.**

Implements `NEW_FEATURES.md` §1–§2. Design, decisions and edge cases live there; this file is only the build order. Delete it (or mark it done) once shipped.

**Out of scope:** collections, stuck signal, daily cap, settings page.

**Commits:** one per step below, each passing `gofmt -l .`, `go vet ./...` and `go test ./...`.

**Branch:** work on a feature branch (e.g. `feature/pause-unpause`). Every push to `main` auto-deploys to the Pi (`.github/workflows/deploy.yml`), so merge only after Step 7 and the verification section are done. Otherwise a half-built feature ships.

**Rules that apply to every step:**
- **Time:** use `s.Now()` / `s.Today()` / `s.today()` (the injected clock and configured timezone), never `time.Now()`. Tests pin the clock with `WithClock`, and SPEC.md §4 requires "today" in the configured timezone.
- **Transactions:** inside `withTx`, every query must go through the `tx`, never `s.db`. The pool has exactly one connection (`db.Open`), so a `s.db` call inside a transaction deadlocks.
- **Dates:** `next_review_date` is stored as `YYYY-MM-DD` text, so comparing those strings lexically is correct (see the `schema.sql` comment). Format new dates the same way.

---

## Step 1 — Schema + migration

- `internal/db/schema.sql`: add `paused_at TEXT` to `review_state` (RFC3339 UTC like `last_reviewed_at`; `NULL` = active). This covers fresh installs.
- `internal/db/db.go`: add `ensurePausedAtColumn(conn)`, called in `Open` right after the schema `Exec` and before `seedTopicsIfEmpty`. It checks `SELECT 1 FROM pragma_table_info('review_state') WHERE name = 'paused_at'` and runs `ALTER TABLE review_state ADD COLUMN paused_at TEXT` only if missing. This covers existing databases.
- **Ordering trap:** `schema.sql` runs *before* the `ALTER`. Nothing in `schema.sql` other than the `CREATE TABLE` column itself may reference `paused_at` (no index, no view). On an existing DB that statement would run while the column doesn't exist yet, and startup would fail. No index is needed at this scale anyway.
- Tests (`db_test.go`):
  - A fresh DB has the column.
  - A DB created with the old `review_state` definition gains the column on `Open`, and existing rows read as `NULL`.
  - Opening the same DB twice is a no-op.

## Step 2 — Service: read side (exclude paused everywhere)

- One shared predicate constant, `activeOnly = "rs.paused_at IS NULL"`.
- Add it to `RecommendDue`, `RecommendUpcoming` (`reviews.go`) and `DueStats` (`stats.go`).
- Rewrite `CountDue` and `UpcomingByDay` (`stats.go`) to query `review_state rs` so the same predicate applies.
- `queryReviewItems` selects `rs.paused_at`. `scanReviewItems` scans it via `sql.NullString` into a new `DueItem.PausedAt *time.Time`. The field goes on `DueItem`, not `ReviewState`, which `RecordReview` returns without reading it.
- `ListProblemsFilter.Status`: `"active"` (default when empty), `"paused"`, `"all"`. `ListLibrary` adds the matching condition. Any other value is treated as `"active"`.
- `libraryOrderBy`: prefix every branch with `(rs.paused_at IS NOT NULL) ASC,`, so paused rows sort last and the existing `rs.problem_id` tiebreaker stays last.
- New `CountPaused(ctx)`.
- Tests:
  - With active and paused problems seeded, all five queries (`RecommendDue`, `RecommendUpcoming`, `CountDue`, `DueStats`, `UpcomingByDay`) exclude paused ones.
  - `ListLibrary` status filter works for all three values.
  - Paused rows sort last under every sort option.
  - `CountPaused` returns the right count.

## Step 3 — Service: write side

- `PauseProblems(ctx, ids []int64) (int, error)`: `UPDATE review_state SET paused_at = ? WHERE problem_id IN (...) AND paused_at IS NULL`, with `paused_at = s.Now().UTC()` in RFC3339. Returns rows affected. An empty `ids` returns 0 without querying. Unknown or duplicate ids are harmless: they simply don't count.
- `UnpauseProblems(ctx, ids []int64, targetPerDay int) (int, error)`, in one transaction:
  1. `SELECT problem_id, next_review_date ... WHERE problem_id IN (...) AND paused_at IS NOT NULL ORDER BY next_review_date ASC, problem_id ASC` (most overdue first; active rows are never touched).
  2. `window = ceil(n / targetPerDay)`, at least 1, with **no** 14-day floor. `targetPerDay <= 0` falls back to `DefaultTargetPerDay`, as in import.
  3. For each row `i` (integer division): `slot = s.Today() + 1 + i*window/n` days, and `newDate = later of (existing next_review_date, slot)`. Worked examples to test against: 20 at 5/day → window 4 → five each on +1..+4; 3 at 5/day → all on +1; 7 at 5/day → window 2 → four on +1, three on +2.
  4. `UPDATE review_state SET paused_at = NULL, next_review_date = ? WHERE problem_id = ?`. Ease, interval and repetitions are untouched.
- `IN (...)` parameters are bounded by the per-page selection (at most 50), well under SQLite's limit.
- Tests:
  - Re-pausing keeps the original `paused_at` and counts 0.
  - Unpausing an active problem changes nothing, including its date.
  - 20 problems at 5/day land 5 each on +1, +2, +3, +4.
  - The later-date rule: after a short pause the original future date is kept; after a long pause the problem gets its slot.
  - Ease, interval and repetitions are unchanged after unpause.
  - **Regression:** `RecordReview` on a paused problem logs the attempt, updates the schedule, and leaves it paused.
  - A bulk-import refresh of a paused problem leaves it paused.

## Step 4 — Add Problem notice for paused problems

- `AddProblemResult.Paused bool`, set when the slug matched an existing paused problem.
- `handleCreateProblem`'s existing re-add notice (`problems.go`) appends that the problem is paused and stays out of rotation.
- Test: re-adding a paused problem records the grade, shows the paused note, and the problem stays paused.

## Step 5 — Handlers

- New route `POST /problems/bulk-status` (`server.go`), handler in `library.go`:
  - Parse `id` values (400 on non-numeric), `action` (`pause` | `unpause`, else 400) and `target_per_day` (bad or empty → default).
  - With no ids selected, redirect back with `done=none` ("No problems selected"), not an error.
  - Redirect with 303 to `/library`, rebuilt from whitelisted params only (`q`, `topic`, `difficulty`, `sort`, `status`, `page`, `page_size`) plus `done=paused|unpaused&n=N`. Never echo a raw URL.
- `handleLibrary`:
  - Parse `status` and pass it to the filter. **Add it to the `extra` pagination params when it isn't the default**, otherwise Prev/Next drop it.
  - Load `CountPaused`, and build the notice string ("Paused 10 problems") from `done`/`n` server-side. Unknown `done` values or a non-numeric `n` show no notice.
- `handleHome` (`due.go`): add the Paused count to the stats.
- `handleDue`: when nothing is due and some problems are paused, pass the paused count for an "N problems paused" note.
- Tests (`httpapi_test.go`):
  - Bulk pause and unpause each redirect with filters preserved and the right notice.
  - Empty selection and a bad action are handled.
  - The status filter survives Prev/Next links.
  - Paused rows under All render "Paused".
  - Home and Due show the paused count.

## Step 6 — Templates + CSS

- `library.html`:
  - Status `<select>` (Active / Paused / All) in the filter toolbar.
  - The table goes inside a separate `<form method="post" action="/problems/bulk-status">` with:
    - a checkbox per row (`name="id"`);
    - a select-all-on-page checkbox (small inline JS; without JS, ticking rows by hand still works);
    - hidden inputs carrying the current filters;
    - **Pause selected** and **Unpause selected** buttons (`name="action"`);
    - a "Target reviews per day" number input next to Unpause (default 5).
  - **Enter-key trap:** pressing Enter in the number input submits the form using its *first* submit button, which would **pause** the selection when you meant to unpause. Block Enter on that input (`onkeydown` → `preventDefault`), the same fix as commit `075cf0e` for the topic filter. Add a test or a manual check.
  - Give each row checkbox an `aria-label` (e.g. "Select Two Sum"), since there's no visible label text.
  - Paused stat card, notice line, paused rows styled (`row-paused`) with a "Paused" badge, and "Paused" in the Next Review column.
- `home.html`: Paused stat card. `due.html`: the "N problems paused" empty-state note.
- `topics.html:23`: rename "Active Problems" to "Problems".
- `layout.html` CSS: reuse existing tokens and components first (`--muted-text`, the existing tag/badge styles) per DESIGN.md's "reuse before adding" rule. Record any new class in DESIGN.md.

## Step 7 — Docs

- `FRONTEND.md`:
  - new "Decided UX facts" entry for Library pause/unpause (multi-select, status filter, reload behavior, paused styling, sort-last);
  - fix #13's "Active Problems" wording;
  - update the page inventory's Library row.
- `NEW_FEATURES.md`: mark §1–§2 as built.
- `DESIGN.md`: any new CSS classes/components.
- `CLAUDE.md` "Commands": the "Migrate: none" line is no longer strictly true. Mention `ensurePausedAtColumn` as a startup column-add step.

---

## Verification

1. `gofmt -l .`, `go vet ./...`, `go test ./...` all clean.
2. **Migration on real data:** copy the Pi's database (from the private backup repo or via `scp`) into the scratchpad, run `go run ./cmd/server -db <copy>`, and confirm it starts and existing problems show as active. Never point a dev run at the only copy.
3. **Manual walk-through on that copy:**
   - Pause 3 problems under Active: they disappear and "Paused 3 problems" shows.
   - Switch to All: they're last, muted, badged "Paused".
   - The nav pill, Due stats and Home's Review Load all drop accordingly.
   - Page through with status=All: the filter persists.
   - Unpause 3 with target 1/day: next reviews are +1/+2/+3, or later if their original dates were later.
   - Re-add a paused problem via Add Problem: the paused note shows and it stays paused.
   - Topics header reads "Problems".
4. **Before deploying:** run `scripts/backup.sh` on the Pi, since this is the first deploy that alters an existing table.
5. **Rollback is safe:** the old binary lists its columns explicitly in every `SELECT` and `INSERT`, so it ignores `paused_at`. Rolling back just makes paused problems reappear in the queue until you redeploy. Nothing is lost.

# DSA Spaced Repetition Tool — Spec (v8)

Supersedes v7. §3: `attempts` now stores the full grade (Failed/Hard/Good/Easy) instead of a collapsed solved/failed flag, so grade history survives `review_state` being overwritten on re-grade. §5: rounding rule specified (round-half-up, once, after the Hard rule and 45-day cap); Failed clarified as an unconditional reset rather than a 0→1 transition. §10: WAL mode + busy_timeout pragma, and a cron-based SQLite backup to a private GitHub repo.

Supersedes v6. §5 fully specified: behavior-anchored grading criteria, grade-dependent first intervals (1/3/5/7 days), multiplicative growth from rep 2, a 45-day interval cap, and explicit call-outs for every deviation from vanilla SM-2. §9's bulk-import default grade changed from Good to Hard.

## 1. Goal & scope

- A personal spaced-repetition layer over LeetCode/NeetCode 150 problems, purpose-built to prevent forgetting earlier topics while progressing through new ones for OA/interview prep
- Does **not** decide what new problem to solve next — purely tracks what's been solved and surfaces what's due
- Explicit design goal: self-hostable by anyone, one instance per person, no shared/multi-tenant deployment
- Secondary project, heavy AI-assistance in the build, one resume bullet as a minor secondary benefit — not the driver of any design decision

## 2. Workflow

1. Open the site — it shows what's overdue for review (optionally filtered by topic, see §8)
2. Either: solve a shown review problem, **or** independently solve a new problem as part of your own NC150 progression
3. Log the result:
   - **Existing problem (a review):** grade it — Failed / Hard / Good / Easy — one click, updates `review_state`
   - **Brand-new problem:** add it via a short form (§9) — this creates the `problems` row _and_ logs the first attempt/grade in the same action
4. Page updates to reflect the new schedule

Grading is always a single, unified action — the grade IS the attempt result, for both new and reviewed problems. A failed attempt still logs and still enters the review loop at a short interval; mastery is the SRS loop's job, not a gate on anything.

Review method is active retrieval — re-solving from scratch, not re-reading an old solution.

## 3. Data model (SQLite)

```sql
problems
  id, title, url, difficulty, slug UNIQUE

attempts                          -- append-only audit log
  id, problem_id, attempted_at, grade (Failed|Hard|Good|Easy)

review_state                      -- one row per problem, once solved once
  problem_id, ease_factor, interval_days, repetitions,
  next_review_date, last_grade, last_reviewed_at

topics
  id, name UNIQUE COLLATE NOCASE

problem_topics                    -- many-to-many
  problem_id, topic_id
```

Notes:

- `slug` is the canonical identifier (e.g. `two-sum`, extracted from whatever LeetCode URL is pasted in) — the unique constraint lives here, not on `url`, since LeetCode URLs vary in form (trailing slash, `/description/` suffix, query params) and a raw-string constraint would silently fail to catch real duplicates. `url` is reconstructed as `https://leetcode.com/problems/{slug}/` for storage/display rather than kept as whatever variant was typed in.
- `topics.name` is case-insensitive unique (`COLLATE NOCASE` in SQLite) so "Linked List" and "linked list" don't silently fork into two tags when problems are added over time by different paths (manual add, bulk import, screenshots)
- `problems` no longer carries `category` or `nc150_order` — topic association is now handled entirely through `problem_topics`, and a problem can carry more than one tag (e.g. a problem can be both "Graphs" and "DFS")
- No `curriculum_progress` table, and no derived "current topic" — there's no curriculum feature left to track progress against
- No separate "weak areas" table — SM-2's ease/interval already resurfaces struggled-with problems sooner
- `attempts` is pure history; `review_state` is live scheduler state, mutated in place
- `attempts.grade` stores the full grade (not a collapsed solved/failed flag) precisely because `review_state.last_grade` is overwritten on every re-grade — the append-only log is the only place grade history over time survives. "Solved" for any downstream purpose (e.g. a future activity heatmap, §12.3) is simply `grade != 'Failed'`, derived on read rather than stored redundantly
- Seed `topics` with the standard NC150 category names as a starting vocabulary (Arrays & Hashing, Two Pointers, Sliding Window, Stack, Binary Search, Linked List, Trees, etc.), but let new tags be created freely when adding a problem — no fixed list

## 4. Scheduling logic

```
Return review_state rows where next_review_date <= today,
most overdue first, optionally filtered to a single topic.
```

That's the entire recommendation engine now — one query, no fallback tiers, no curriculum-order logic. If nothing is due, there's nothing to show; that's an expected, normal state, not an error case — go solve something new on your own and log it via §9.

"Adaptive workload" remains a property of the interaction model, not a stored setting — visit the page as often as there's time for.

**Timezone note:** compute "today" in your own local timezone (SGT), not the server's. This matters concretely if the GCP fallback host (§10) is ever used — a US-region server naively using its own local time, or UTC, could shift the day boundary by several hours relative to when you actually sit down to practice, making a review appear due earlier or later than it should. Store `next_review_date` as a date, and derive "today" from a fixed timezone in the app config rather than the host's local clock.

## 5. Algorithm: SM-2, with deliberate deviations (FSRS rejected for v1)

**Grading criteria** — behavior-anchored, not a vague self-report, to resist drifting over months:

| Grade  | Criteria                                                                      |
| ------ | ----------------------------------------------------------------------------- |
| Failed | Could not solve independently, or needed the solution / major hints           |
| Hard   | Solved only after a meaningful hint, or with substantial struggle/uncertainty |
| Good   | Solved independently, no meaningful hints, normal problem-solving effort      |
| Easy   | Solved independently, quickly and confidently, immediate pattern recognition  |

Guideline ceiling for grading **Easy** specifically (not a rule for the other three grades, and not a timer feature — this is a mental rule of thumb applied when choosing a grade, nothing in the app measures elapsed time): Easy problems <10 min, Medium <20 min, Hard <30 min.

**State per problem:** `ease_factor` (starts 2.5, floor 1.3), `interval_days`, `repetitions` (resets to 0 only on Failed — Hard still counts as a pass and increments it).

**First interval (rep 0→1)** — this is the first deliberate deviation from vanilla SM-2, which gives a flat 1 day after any success regardless of grade. Here, the grade sets the first interval directly:

| Grade  | First interval | Ease effect           |
| ------ | -------------- | --------------------- |
| Failed | 1 day          | −0.20, floored at 1.3 |
| Hard   | 3 days         | −0.15, floored at 1.3 |
| Good   | 5 days         | unchanged             |
| Easy   | 7 days         | +0.15                 |

This applies whenever `repetitions` transitions 0→1 — a brand-new problem's first grade, and equally the first successful re-grade after a Failed reset (a reset restarts the full tiering from scratch, with no memory of the old interval).

**Failed is an unconditional reset, not a 0→1 transition itself.** Grading Failed always sets `interval_days` to 1 and `repetitions` to 0, regardless of what `repetitions` was beforehand — including a second (or Nth) consecutive Failed, where `repetitions` simply stays at 0 and ease keeps dropping by 0.20 (floored at 1.3) each time. The Failed row above only shares a table with the other three grades for presentation; it is not gated on the 0→1 transition the way Hard/Good/Easy are.

**Second interval onward (rep 1→2 and beyond): multiplicative, not vanilla SM-2's flat 6 days** — second deviation, and a required one: a flat 6-day second tier is incoherent once the first interval is grade-dependent (Easy's 7-day first interval would otherwise _shrink_ to 6). From rep 2 onward: `interval = previous_interval × ease_factor`, except Hard, which is capped at `previous_interval × 1.2` regardless of ease — Hard should stay in tight rotation, not compound like a confident grade.

**Hard cap: `interval_days` never exceeds 45** — third deviation; vanilla SM-2 has no ceiling. Justified specifically by a bounded ~1-year prep window and the explicit goal of not losing early topics — unbounded growth (a well-graded problem could otherwise stretch past a year between reviews) directly reproduces the forgetting problem this tool exists to prevent. The cap is a ceiling applied to whatever the formula above produces, not a separate per-grade rule — it only ever binds on sustained Good/Easy paths; consistently-Hard problems top out around 7 days on their own and never approach it.

**Rounding rule:** keep every intermediate value (the raw multiplicative interval, the Hard ×1.2 rule, the 45-day cap comparison) as a float, and round to the nearest whole day exactly once, as the final step, using round-half-up (round half away from zero — e.g. Go's `math.Round`). Never round an intermediate value before applying the cap or the Hard rule.

**⚠️ Correctness-critical module — do not trust AI-generated tests blindly here.** Hand-check generated code against this worked trace (ease starting 2.5, Good/Good/Good/Hard/Good):

| Review | Grade | Interval                                | Ease after |
| ------ | ----- | --------------------------------------- | ---------- |
| 1      | Good  | 5 days                                  | 2.5        |
| 2      | Good  | 13 days                                 | 2.5        |
| 3      | Good  | 33 days                                 | 2.5        |
| 4      | Hard  | min(33×2.5, 33×1.2) = 40 days           | 2.35       |
| 5      | Good  | min(40×2.35, 45 cap) = 45 days (capped) | 2.35       |

The two easiest places for generated code to diverge from this: resetting `repetitions` on Hard (it shouldn't), and applying the cap before rather than after computing the raw multiplicative interval (order matters once both the ×1.2 Hard rule and the 45-day cap can apply to the same review).

FSRS remains rejected for v1 — a better algorithm in principle, but its trained-model/optimizer step is real complexity this personal tool doesn't need; SM-2 with the above adjustments is a pure function, fully unit-testable.

## 6. Service / API boundary

- Clean internal service layer: `RecommendDue(topic *string)`, `AddProblem(...)`, `RecordAttempt(...)`, `RecordReview(...)`, `ListTopics()`
- Thin HTTP handlers wrap the service layer — needed since the web frontend calls it over HTTP from two machines
- No authentication / multi-tenancy in the app layer — Tailscale network membership is the access control (§10)

## 7. Client: web frontend (no framework)

- Server-rendered HTML via Go's `html/template`; **htmx** for interactivity (grading buttons, swapping in the next due item, the add-problem form, edit/delete actions) — attributes only, no build pipeline, no npm, no React
- htmx embedded via `go:embed` — no CDN dependency
- One Go binary serves both API and HTML
- Problem links use named-window targeting (`target="leetcode"`) so clicking through always reuses the same browser tab instead of spawning new ones — the reason this became a web app instead of a TUI in the first place
- Topic filter (a dropdown next to the due-list) — ships in v1; the full multi-topic quota composer does not (§11)
- **Library view** — a separate page from the due-queue: every problem you've logged, filterable by topic (and optionally difficulty), each row with edit/delete affordances. This is the direct answer to "let me see what I've solved from a given topic" — a distinct browsing view, not just a filter on top of the due-list.

## 8. Topic tagging

- Many-to-many (§3) — a problem can carry multiple tags
- Full CRUD on topics: create, rename, delete. Deleting a topic removes the tag association from any problems that carried it — it does not delete the problems themselves
- Powers, in v1: filtering the due-list and the library view (§7) to a single topic
- Deliberately built now to lay the foundation for the topic-quota review composer (§11) — the schema needs to exist before that feature can be built, even though the feature itself is deferred

## 9. Managing problems (CRUD)

Two entry points into creating a problem, plus edit/delete on any existing one:

**Single add (ongoing use):**

- A short form: title, URL, difficulty, one or more topic tags
- Optional, non-blocking convenience: paste a LeetCode URL and auto-fetch title/difficulty via LeetCode's public, unauthenticated GraphQL endpoint (no session/login needed for problem metadata), falling back to manual entry if the lookup fails — never a hard dependency, since self-hosters shouldn't need a working scraper for basic functionality
- Before inserting, normalize the URL to its slug (e.g. `two-sum`) rather than matching on the raw URL string — LeetCode URLs vary in form (trailing slash, `/description/` suffix, etc.), so raw-string matching would silently defeat the dedupe check below. If the slug already exists in `problems`, this should record another attempt/grade on the existing row rather than creating a duplicate — the same rule bulk import follows, just triggered one row at a time.

**Bulk import (seeding — first-run cold start):**

- CSV/paste with columns: title, url, difficulty, topics
- Purpose: bootstrap existing solve history (e.g. Zade's ~180 already-solved problems) without 180 manual form entries — and the same cold-start problem applies to any self-hoster starting from zero, so this is a self-hosting UX feature, not just a personal convenience
- **Recommended source:** LeetCode's own Problems list, filtered by Status: Solved — this view already shows title, difficulty, and topic tags per row, so a handful of screenshots handed to an AI assistant for transcription into the CSV format covers everything needed in one pass. No authenticated script or session cookie required for this path.
  - Fallback, only if the screenshot approach is impractical: a one-off personal script using LeetCode's authenticated endpoint (e.g. the `leetcode-export` package) to pull your own solved-problems list directly, run once and discarded. This is distinct from ongoing, live auto-submission-tracking (explicitly out of scope — see §13) — that would be a persistent runtime dependency; this is a single run to generate the import CSV. If used, do not carry problem descriptions into the CSV or the repo — LeetCode's problem text is not yours to redistribute, and the schema never needed it anyway (title/URL/difficulty/topics only).
- **Dedupe on import: key on slug, not raw URL** (see the normalization note above) — re-importing or overlapping rows should upsert against the existing problem rather than create a duplicate
- Each row is graded once as a baseline **Hard** (not Good) — deliberately conservative, since a bulk-imported backlog isn't the same as confidently-retained material; you may not be able to re-solve everything on day one, and the import shouldn't assume otherwise. Dated at import time. Solve-date isn't available from the screenshot method, and isn't needed given this baseline-grade approach — subsequent honest re-grading corrects the state as each problem actually comes up.
- **Stagger initial `next_review_date` across the next ~2 weeks by import order rather than defaulting every row to the same date** — without this, all imported problems become "overdue" simultaneously, producing a pile-up on day one that directly contradicts the adaptive-workload design in §4
- **Verify before committing:** since transcription (AI-assisted or manual) of ~180 rows is error-prone, spot-check row count and look for duplicate/missing problem numbers before running the insert — cheaper to catch here than after
- Metadata autofill for the single-add path: check a small bundled offline reference table (title/difficulty/topics for common NC150/Blind75 problems, keyed by slug) before falling back to a live lookup or manual entry

**Edit / delete:**

- Any problem's fields (title, URL, difficulty, tags) can be edited after creation
- Delete removes the problem and cascades to its `attempts` and `review_state` rows
- Needed from day one, not a later nice-to-have — a bulk import of 180 AI-transcribed rows will almost certainly need a few corrections, and there's no other way to fix a bad row without editing SQLite directly

## 10. Hosting & networking

**Primary host:** the Raspberry Pi 4 Model B (4GB), already purchased, likely shared with Observable Career Tracker if there's headroom — this app's footprint is negligible (~50MB idle) by comparison. Boot from an external USB SSD rather than microSD.

**Fallback:** GCP `e2-micro`, Always Free tier — $0/mo, permanent — if the Pi ends up too tight or isolation from Career Tracker is preferred.

**Access:** Tailscale-only, from both desktop and laptop (each dual-booting Windows/Linux) — local-only storage would fragment review state across environments given regular switching between machines.

- App binds to the Tailscale interface, not `0.0.0.0`
- Firewall denies public inbound except SSH (key-only)
- Packaging: single Docker container, Go binary + mounted SQLite volume
- SQLite connection opens with `journal_mode=WAL` and a `busy_timeout` (e.g. 5000ms). True concurrent writes are not expected (single user, two personal devices) so no app-level write-queueing or locking is implemented — this pragma pair is just cheap insurance against an incidental overlapping request (e.g. a stale browser tab), turning it into a brief wait instead of a dropped request

**Backups:** a cron job periodically runs `sqlite3 <db path> ".backup backup.db"` (the built-in hot-backup command, safe to run against a live WAL-mode database) and pushes the result to a private GitHub repo. Storage growth is not a concern for this dataset's scale (low-thousands of rows at most over years of use), so no pruning/retention policy is needed.

## 11. Deferred (decided, scoped, but not v1)

- **Topic-quota review composer** — e.g. "give me 1 Stack, 1 Array/Hashmap, 1 LinkedList" for a session. Explicitly deferred until everything else here is complete. Topic tagging (§8) is being built now specifically so this has a foundation to sit on later, without requiring a schema change when it's picked up.

## 12. Open questions (unresolved — flag if reviewed by another AI)

From User: I will answer/deal with this after this project has been completed and is usable first.

1. **Variant-swapping** — after a problem's initial repetitions, should reviews swap in a same-pattern problem instead of repeating the identical one indefinitely? Topic tagging (§8) opens a plausible mechanism — substitute a different problem sharing a tag from your own solved set — but this isn't decided or designed yet.
2. **Mastered-problem audits** — once a problem's interval grows very long, consider forcing an occasional spot-check re-attempt rather than letting SM-2 intervals grow unbounded (borrowed from prior art).
3. **Stats/heatmap view** — attempts-per-day calendar, cheap to add on `attempts`, good for motivation. Not required for v1.

## 13. Explicitly out of scope

- Any curriculum-guidance feature (auto-recommending the next new problem, NC150 ordering, derived "current topic") — deliberately removed, not merely unbuilt
- Postgres (SQLite is correct for single-writer, personal-scale data)
- Multi-tenancy / login / per-user auth
- TUI, Discord, ntfy, email, Telegram as interfaces
- Per-problem notes/annotations
- Offline support / sync beyond the hosted model in §10
- LeetCode API as a live runtime dependency beyond the optional add-problem lookup in §9, or automatic submission tracking
- Push notifications by default (user self-initiates sessions)

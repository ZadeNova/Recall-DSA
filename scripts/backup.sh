#!/usr/bin/env bash
# Backs up the live SQLite database and pushes it to a private git repo
# (SPEC.md §10). Intended to run on the host via cron, not inside the
# container — the distroless image has no shell or sqlite3 binary.
#
# Usage: backup.sh <path-to-recall.db> <path-to-cloned-backup-repo>
#
# <path-to-cloned-backup-repo> must already be a git repo cloned on this
# machine with push access configured (SSH deploy key or PAT) — this
# script never touches credentials itself.
set -euo pipefail

DB_PATH="${1:?usage: backup.sh <db-path> <backup-repo-path>}"
BACKUP_REPO="${2:?usage: backup.sh <db-path> <backup-repo-path>}"

# SQLite's own .backup command, safe to run against a live WAL-mode
# database (unlike copying the file directly, which could grab it
# mid-write).
sqlite3 "$DB_PATH" ".backup '$BACKUP_REPO/backup.db'"

cd "$BACKUP_REPO"
git add backup.db
# One overwritten file, committed every run — git history is the
# restore-point history, so there's no unbounded growth from
# accumulating dated snapshots (a diff between two runs is small even
# when logically nothing changed, since git compresses it well; the
# repo does NOT grow one full backup.db per day). In practice this
# commits every run, even with no real review activity: SQLite's own
# header (a page-level change counter) mutates on every .backup call
# regardless of logical content, so two backups are essentially never
# byte-identical. The `|| exit 0` below is still worth keeping as a
# defensive no-op guard (e.g. an unwritable repo checkout with nothing
# staged), it just won't be the common case.
git commit -m "Backup $(date -u +%FT%TZ)" --quiet || exit 0
git push --quiet

# Damast

Soft fork of Forgejo (upstream: https://codeberg.org/forgejo/forgejo). Must stay a drop-in replacement, in both directions.
Docs, designs and tasks live in Jira project **MAST** (jinc.atlassian.net), not in this repo.

## Hard rules

1. **Stay merge-friendly.** Own code goes in own files/packages (`services/damast/...`, `models/damast/...`, `modules/damast/...`).
   Upstream files get only minimal hooks (a few lines), each marked `// DAMAST:` (templates: `{{/* DAMAST: */}}`).
   Never reformat or reorder upstream code.
2. **Never change upstream tables or the upstream migration chain** (`models/migrations`, `models/gitea_migrations`,
   `models/forgejo_migrations*`). Forgejo refuses to start if `forgejo_migration` contains unknown IDs.
   New data goes in `damast_*` tables in `models/damast`, registered with `db.RegisterModel`; Forgejo's
   `SyncAllTables` creates them on every start. A separate `damast_migration` track (MAST-12) is only added
   once a real data migration is needed.
   New config goes in an `[damast]` section of `app.ini` only.
3. **No Forgejo branding** in our name/logo (trademark). Keep attribution and license (GPL-3.0-or-later).
4. Code, comments and commit messages in English. Commits end with
   `Co-authored-by: Claude <noreply@anthropic.com>`.
5. Existing tests stay green; new features come with tests.

## Upstream sync

`main` = a Forgejo release tag + Damast commits. New releases arrive as `sync/vX.Y.Z` PRs
(`.github/workflows/upstream-sync.yml`). Merge them with a **merge commit**; never squash or rebase them.
Releases are tagged `vX.Y.Z-damast.N`.

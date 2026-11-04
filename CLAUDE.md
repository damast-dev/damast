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
6. **Only complete features ship.** Work in phases if useful, but a feature is merged to `main` only when it is
   fully done: every part of its design, permission checks, tests (unit + integration, E2E where there is UI
   behaviour), and the way back to Forgejo documented. Half features stay on their branch.
7. **Every feature is presented.** The same PR updates `.github/README.md` (what's different, going back table,
   roadmap). After merge, damast.dev gets a section with real screenshots (repo `damast-dev/damast.dev`).
8. **Reviewed before merge.** Each feature PR gets at least one independent review that actively tries to break
   it (permissions, data leaks, edge cases, upstream merge risk). Security-relevant features get a second,
   security-only review. Findings are fixed or answered in the PR before it is merged.
9. **CLI too.** If a feature makes sense on the command line, the same feature ships a `mast` command
   (repo `damast-dev/mast`, see its CLAUDE.md). A feature is only complete when both are done.

## Upstream sync

`main` = a Forgejo release tag + Damast commits. New releases arrive as `sync/vX.Y.Z` PRs
(`.github/workflows/upstream-sync.yml`). Merge them with a **merge commit**; never squash or rebase them.

## Releases

Tags: `v<forgejo>-damast.N` (final) and `v<forgejo>-damast.N-rc.M` (release candidate). `N` counts Damast
releases on one Forgejo version and restarts at 1; the Forgejo version stays in front because the binary
reports it to the database (`forgejo_sem_ver`) and the API. Logic and self-test: `.github/scripts/version.sh`.

- `tag-rc.yml` tags the next RC when a sync PR is merged, or by hand after Damast features land.
- `promote.yml` tags the final release on the RC's commit. Never tag by hand.
- `release.yml` builds linux amd64/arm64 binaries and `ghcr.io/damast-dev/damast` images per tag. RCs only
  get their exact image tag; finals also move `X.Y.Z`, `X.Y`, `X` and `latest`.

Every PR gets one label for the release notes: `feature`, `fix`, `security`, `branding`, `ci`,
`upstream-sync` or `skip-changelog`.

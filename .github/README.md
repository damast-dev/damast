# Damast

**A soft fork of [Forgejo](https://forgejo.org) that stays a drop-in replacement.** Website: [damast.dev](https://damast.dev) · CLI: [`mast`](https://github.com/damast-dev/mast)

Damast tracks every Forgejo stable release and adds a small set of features on top.
You can switch from Forgejo to Damast, and back, by swapping the binary/image. Same `app.ini`, same data, same database.

## What's different

| Feature | Forgejo | Damast |
|---|---|---|
| `Co-authored-by:` trailers | shown as plain text | co-authors shown and credited on commits |
| Package registry | owned by user/org, optionally linked to a repo | bound to the repository (GitHub-style), repo permissions apply |

## Rules of the fork

1. **Base is always a Forgejo release tag.** Current base: `v17.0.1`. A daily workflow opens a `sync/vX.Y.Z` PR for every new upstream release.
2. **Never touch Forgejo's migration chain** (`models/migrations`, `models/forgejo_migrations`). Extra data lives in `damast_*` tables, which Forgejo ignores. CI enforces this.
3. **Small diff.** New code in new files, minimal hooks into upstream files. Upstream files are never reformatted.
4. **Upstream first.** Anything Forgejo would accept is proposed upstream too.

## Going back to Forgejo

Stop Damast, start the Forgejo binary/image of the **same version** that Damast is based on, using the same config and data. Optionally drop the `damast_*` tables. Damast-only data (co-author credit cache, repo-scoped package permissions) is lost, but nothing from Forgejo is.

## License

GPL-3.0-or-later, like Forgejo. Damast is not affiliated with or endorsed by the Forgejo project.

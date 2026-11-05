<p align="center"><img src="../assets/logo.svg" width="112" alt="Damast logo"></p>
<h1 align="center">Damast</h1>
<p align="center"><b>Every commit a layer.</b><br>Self-hosted git with the GitHub habits your team already has. Built on Forgejo.</p>
<p align="center"><a href="https://damast.dev">Website</a> · <a href="#switching-from-forgejo">Switch from Forgejo</a> · <a href="#going-back-to-forgejo">Go back</a> · <a href="https://github.com/damast-dev/mast">mast CLI</a> · <a href="#where-damast-is-going">Roadmap</a></p>

> [!NOTE]
> Damast is a soft fork of [Forgejo](https://forgejo.org). It is currently based on **Forgejo v17.0.1**. Every new Forgejo release arrives here as a pull request within a day, and ships as a Damast release of the same version.

> [!TIP]
> **Already running Forgejo?** Change one image name and restart. Same database, same `app.ini`, same repositories.
> **Not for you?** Change it back. Forgejo starts on the same data, because Damast never touches Forgejo's tables.

## Why Damast exists

Most of us learned to work together on GitHub. Pull requests, `Co-authored-by` trailers, packages that sit next to the code they came from, workflows that publish with their own token. Nobody thinks of these as features anymore. They are habits.

When a team moves to a self-hosted forge, every habit that doesn't carry over turns into a small daily papercut. Someone pairs on a fix and their name disappears. A contributor can push code but can't publish the package it builds. A workflow needs a personal access token for something GitHub does out of the box. None of these is a big deal alone. Together they are the reason teams drift back.

Forgejo is a great forge with ideas of its own. It is built around independence, community governance and federation, and it deliberately doesn't try to be GitHub. That is a good choice for Forgejo, and the reason it exists.

Damast makes the other choice. **Where Forgejo and GitHub work differently, Damast leans toward GitHub.** Not the look or the branding, the way things work: who gets credit, who can publish, what a token is allowed to do. If your team relies on it on GitHub, it belongs in Damast.

That is an opinion, and plenty of Forgejo users won't share it. That's fine. Damast doesn't ask you to give up Forgejo. It is Forgejo with a different set of defaults on top, and you can take them off again.

### Why "Damast"

*Forgejo* is Esperanto for *forge*. *Damast* is German for Damascus steel, what comes out of a good forge when you fold layers of steel together until each one shows in the blade. That is how this fork is built: every Damast release is a Forgejo release with a thin, visible layer on top. Every commit a layer.

## What's different

Each change starts with something that worked on GitHub and didn't on Forgejo.

### Co-authors get the credit

**On Forgejo:** `Co-authored-by:` trailers show up as plain text at the bottom of the commit message. Pair programming, mob sessions and suggested changes all end up credited to one person.

**On Damast:** every co-author appears next to the author, with avatar and a link to their profile, on the commit page and in every commit list. Co-authors without an account still show, with the avatar for their email. There is nothing to configure: write the trailer, Damast does the rest.

### Packages belong to their repository

**On Forgejo:** a package belongs to a user or an organization. Only owners can publish, so a collaborator who can push to a repository can't publish what it builds. Actions workflows need a personal access token to publish at all.

**On Damast:** a package belongs to the repository it was built from, like on GitHub.

- New packages link to their repository by themselves: from the Actions run that published them, from the repository URL in their metadata, or from their name.
- Write access to the repository lets you publish. Read access lets you download, even from a private organization.
- Workflows publish with their built-in token. Pull requests from forks can only read.
- Someone who can't read a package gets a `404`, not a `403`, so names of packages in private repositories don't leak.
- One switch per package turns this off. Packages that existed before you switched keep Forgejo's rules.

### Suggested changes in reviews

**On Forgejo:** a reviewer who knows the fix writes it into a comment, and the author copies it into their editor, commits and pushes. Small fixes take a round trip each.

**On Damast:** write a ` ```suggestion ` block in a review comment on a line or a range of lines, like on GitHub. The "Suggest" button in the comment editor fills it with the commented lines for you.

- The comment shows the suggestion as a small diff of the old and the new lines.
- Whoever can push to the pull request's branch gets **Commit suggestion**, or **Add suggestion to batch** to collect several and commit them as one commit. That includes maintainers of a fork's pull request if its author allows edits by maintainers. Branch protection applies as for any push.
- The commit message is "Apply suggestions from code review" (you can change it) and credits every reviewer whose suggestion is in it with a `Co-authored-by:` trailer, so they show up as co-authors.
- Committed conversations are resolved. A suggestion whose lines changed in the meantime is outdated and can't be committed anymore; suggestions in one batch can't touch the same lines.
- You only commit what you've seen: a suggestion edited after you loaded it is refused. LFS pointers, files too large for the web editor, suggestions over 64 KiB and batches over 100 suggestions are refused too.
- The API has it too: `GET /api/v1/repos/{owner}/{repo}/pulls/{index}/damast/suggestions` lists suggestions, `POST …/damast/suggestions/apply` commits them (with the `content_version` of each one from the list), and [`mast pr suggestions`](https://github.com/damast-dev/mast) uses both. A token limited to some repositories or to public ones must cover the pull request's head repository.

### Its own name

Damast says Damast in the UI, with its own logo, and still links to Forgejo in the footer. Forgejo's name and logo are Forgejo's trademark, so a fork that ships its own changes shouldn't use them.

Everything else is Forgejo, unchanged: repositories, issues, pull requests, Actions, wikis, the API, federation work, the admin tools, the [documentation](https://forgejo.org/docs/latest/).

## Is Damast for you?

Damast is new and opinionated. Here is the honest version.

**Use Damast if** your team came from GitHub, or still works there too, and the papercuts above are real for you. Or if you run Forgejo already and want these features without a migration you can't undo.

**Stay on Forgejo if:**

- **You chose Forgejo because it isn't GitHub.** Damast is heading the other way, and you won't like where it goes.
- **You want a long track record and a big community.** Forgejo has years of releases, upgrade tests and a security team. Damast started in November 2026 and has a small team. If you want a place where your question has already been answered, that place is Forgejo.
- **You need security fixes the hour they're out.** Damast follows every Forgejo release, but a fix still has to pass through our sync, a release candidate and the final release. It reaches you later than it reaches Forgejo users.
- **You don't need any of the changes.** That's the most common reason, and a good one. If Forgejo already does what you want, it is leaner, and more people are looking at its code.

## Switching from Forgejo

Damast `vX.Y.Z-damast.N` is built on Forgejo `vX.Y.Z`. Switch to the Damast release that matches your Forgejo version, or a newer one.

1. **Back up.** `forgejo dump`, or a copy of your database, data directory and `app.ini`. Damast doesn't rewrite your data, but you should always have a backup before you change the binary.
2. **Swap the image.** Docker tags follow Forgejo's, so you only change the registry:
   ```diff
   - image: codeberg.org/forgejo/forgejo:17
   + image: ghcr.io/damast-dev/damast:17
   ```
   Rootless works the same way (`:17-rootless`). Running the binary? Replace it with `damast-<version>-linux-<arch>` from the [releases](https://github.com/damast-dev/damast/releases). Keep the name `gitea` or `forgejo`, whatever your service file and git hooks use.
3. **Start it.** Damast creates its own `damast_*` tables on first start. That's it.

## Going back to Forgejo

The door swings both ways by design.

1. **Check your version.** Damast `v17.0.1-damast.2` means Forgejo `v17.0.1`. Go back to that Forgejo version or a newer one, never an older one.
2. **Back up**, stop Damast, and start Forgejo on the same data and `app.ini`.
3. **Optional:** drop the `damast_*` tables. Forgejo ignores them either way.

What you lose:

| Feature | After going back |
|---|---|
| Co-authors | The trailers stay in your commits (that's git). Forgejo shows them as text again. |
| Suggested changes | Suggestions stay in the comments as code blocks, and commits made from them keep their trailers. The buttons are gone, so the author applies them by hand again. |
| Repo-bound packages | Packages, files and their repository link stay. Access falls back to Forgejo's owner rules, so people who could only publish or download through the repository lose that. Check this before you switch. |

Nothing from Forgejo is lost, because Damast never changes Forgejo's tables or its migration chain. CI rejects any pull request that tries.

## Versions

```
v17.0.1-damast.2-rc.1
 ╰─────╯       ╰╯  ╰──╯
 Forgejo    Damast  release candidate
```

- **`N`** counts Damast releases on one Forgejo version, and starts at 1 again with each new Forgejo version.
- **The Forgejo version comes first** because the binary reports it to your database and to the API. Forgejo's own startup checks and API clients read it, so it has to be the real Forgejo version.
- **Release candidates** (`-rc.M`) come first. When an RC holds up, the same commit is tagged as the final release. RCs are GitHub pre-releases and only get their exact Docker tag.
- **Docker tags** on finals: `17.0.1-damast.2`, `17.0.1`, `17.0`, `17` and `latest`, plus `-rootless` variants. `17.0.1` always points to the newest Damast release on Forgejo 17.0.1.

## Where Damast is going

The direction is GitHub's way of working, inside the limits of the drop-in promise: if a feature would need to change Forgejo's tables, it waits or finds another way.

**Shipped**
- Co-authors on commit pages and in commit lists
- Repo-bound packages: publish, download, Actions token, per-package switch
- Suggested changes in reviews: commit one or a batch, reviewers credited as co-authors
- Damast's own name and logo
- Releases with release candidates, binaries for amd64 and arm64, Docker images

**Next**
- Co-authors in the contribution graph and on profiles, so pairing counts there too
- An automated test on every Forgejo sync: switch a real instance to Damast and back, and check that Forgejo starts cleanly
- [`mast`](https://github.com/damast-dev/mast) commands for Damast's features, starting with packages

**Your idea?** What do you miss from GitHub? [Open an issue](https://github.com/damast-dev/damast/issues/new) and tell us what your team trips over.

## How the fork works

For contributors, and for anyone wondering how a fork stays this close:

1. **Every release starts from a Forgejo release tag.** `main` is a Forgejo tag plus Damast's commits. A daily workflow opens a `sync/vX.Y.Z` pull request for each new Forgejo release, and it is merged with a merge commit, never rebased.
2. **Never touch Forgejo's migration chain.** Forgejo refuses to start on a database with migrations it doesn't know. Damast's data lives in its own `damast_*` tables; settings go in a `[damast]` section of `app.ini`.
3. **Small diff.** New code lives in its own packages (`services/damast`, `models/damast`, `modules/damast`). Forgejo's files only get small hooks marked `// DAMAST:`, and are never reformatted. The less we change, the faster we follow.
4. **Upstream first.** Anything Forgejo would want goes to Forgejo too. When Forgejo ships its own version of a feature, Damast drops its own.

## Contributing

Bug reports, ideas and pull requests are welcome. Keep a pull request to one change, add tests for new behaviour, and give it one label for the release notes (`feature`, `fix`, `security`, ...). The rules above are checked in review, and the migration rule is checked in CI. [`CLAUDE.md`](../CLAUDE.md) has the details, including how releases are cut.

Found a bug that also happens on Forgejo? Report it to [Forgejo](https://codeberg.org/forgejo/forgejo/issues), so everyone gets the fix.

## How Damast is built

Damast is written with a lot of help from Claude Code. Humans decide what Damast should be, review every change and test the releases. You'll find it in the commits as a `Co-authored-by: Claude` trailer, which, fittingly, Damast shows. Judge the result: read the code and the tests.

## Thanks, Forgejo

Damast exists because Forgejo exists. The forge, the Actions runner, the package registry, the security work and every release we build on come from the Forgejo contributors and from Gitea before them. If Damast is useful to you, support [Forgejo](https://forgejo.org) and [Codeberg e.V.](https://codeberg.org), its home, too.

Damast is not affiliated with or endorsed by the Forgejo project.

## License

GPL-3.0-or-later, like Forgejo. See [LICENSE](../LICENSE).

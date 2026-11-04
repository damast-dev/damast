// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package draft implements GitHub-style draft pull requests on top of
// Forgejo's work-in-progress title prefix ([repository.pull-request] WORK_IN_PROGRESS_PREFIXES).
// The prefix stays the only storage, so plain Forgejo still sees (and blocks) the same PRs.
package draft

import (
	"strings"

	"forgejo.org/modules/setting"
)

// Prefix returns the WIP prefix the title starts with (as written in the title), or "".
func Prefix(title string) string {
	upper := strings.ToUpper(title)
	for _, p := range setting.Repository.PullRequest.WorkInProgressPrefixes {
		if p != "" && strings.HasPrefix(upper, strings.ToUpper(p)) {
			return title[:len(p)]
		}
	}
	return ""
}

// IsDraft reports whether the title marks a draft pull request.
func IsDraft(title string) bool {
	return Prefix(title) != ""
}

// Strip returns the title without its WIP prefix, for display. A title that is only a prefix is kept.
func Strip(title string) string {
	p := Prefix(title)
	if p == "" {
		return title
	}
	if rest := strings.TrimSpace(title[len(p):]); rest != "" {
		return rest
	}
	return title
}

// Mark returns the title with the first configured WIP prefix, unless it already has one.
func Mark(title string) string {
	prefixes := setting.Repository.PullRequest.WorkInProgressPrefixes
	if len(prefixes) == 0 || IsDraft(title) {
		return title
	}
	return strings.TrimSpace(prefixes[0]) + " " + strings.TrimSpace(title)
}

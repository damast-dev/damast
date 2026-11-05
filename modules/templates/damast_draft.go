// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import "forgejo.org/modules/damast/draft"

// DamastIsDraftPull reports whether a pull request title marks a draft. Issues are never drafts.
func DamastIsDraftPull(title string, isPull bool) bool {
	return isPull && draft.IsDraft(title)
}

// DamastPullTitle returns the title to display: draft pull requests without their WIP prefix.
func DamastPullTitle(title string, isPull bool) string {
	if !isPull {
		return title
	}
	return draft.Strip(title)
}

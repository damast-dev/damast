// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"io"

	"forgejo.org/modules/git"
)

// PolicyPaths are the places of a security policy, in the order they are tried: Forgejo's own folders first
// (as for issue templates), then GitHub's order (.github, root, docs).
var PolicyPaths = []string{".forgejo/SECURITY.md", ".gitea/SECURITY.md", ".github/SECURITY.md", "SECURITY.md", "docs/SECURITY.md"}

const maxPolicySize = 1 << 20

// FindPolicy returns the path and content of the security policy in the commit, or "" if there is none.
func FindPolicy(commit *git.Commit) (string, []byte, error) {
	if commit == nil {
		return "", nil, nil
	}
	for _, p := range PolicyPaths {
		entry, err := commit.GetTreeEntryByPath(p)
		if err != nil {
			if git.IsErrNotExist(err) {
				continue
			}
			return "", nil, err
		}
		if !entry.IsRegular() {
			continue
		}
		r, err := entry.Blob().DataAsync()
		if err != nil {
			return "", nil, err
		}
		content, err := io.ReadAll(io.LimitReader(r, maxPolicySize))
		r.Close()
		return p, content, err
	}
	return "", nil, nil
}

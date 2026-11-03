// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"context"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/damast/coauthor"
	"forgejo.org/modules/git"
)

// DamastCoAuthor is a co-author of a commit, with the matching user if any.
type DamastCoAuthor struct {
	coauthor.Identity
	User *user_model.User
}

// DamastCoAuthors returns the co-authors of a commit for rendering.
// shortcut: one user lookup per co-author and commit (no request cache); add a cache if commit lists get slow.
func DamastCoAuthors(ctx context.Context, c *git.Commit) []DamastCoAuthor {
	if c == nil {
		return nil
	}
	authorEmail := ""
	if c.Author != nil {
		authorEmail = c.Author.Email
	}
	ids := coauthor.Parse(c.CommitMessage, authorEmail)
	out := make([]DamastCoAuthor, 0, len(ids))
	for _, id := range ids {
		u, _ := user_model.GetUserByEmail(ctx, id.Email)
		out = append(out, DamastCoAuthor{Identity: id, User: u})
	}
	return out
}

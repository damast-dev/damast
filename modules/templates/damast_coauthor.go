// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"context"
	"strings"

	user_model "forgejo.org/models/user"
	"forgejo.org/modules/cache"
	"forgejo.org/modules/damast/coauthor"
	"forgejo.org/modules/git"
)

// DamastCoAuthor is a co-author of a commit, with the matching user if any.
type DamastCoAuthor struct {
	coauthor.Identity
	User *user_model.User
}

// damastUserByEmail resolves an email like Forgejo resolves commit authors, cached per request
// (misses too, so a trailer repeated on every commit costs one lookup per page).
func damastUserByEmail(ctx context.Context, email string) *user_model.User {
	u, _ := cache.GetWithContextCache(ctx, "damast_coauthor_user", strings.ToLower(email), func() (*user_model.User, error) {
		u, _ := user_model.GetUserByEmail(ctx, email)
		return u, nil
	})
	return u
}

// DamastCoAuthors returns the co-authors of a commit for rendering, one entry per person:
// co-authors that resolve to the author's account or to an account already listed are dropped.
func DamastCoAuthors(ctx context.Context, c *git.Commit) []DamastCoAuthor {
	if c == nil || c.Author == nil { // the templates render the author's name and email next to the stack
		return nil
	}
	authorEmail := c.Author.Email
	ids := coauthor.Parse(c.CommitMessage, authorEmail)
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]bool{}
	if a := damastUserByEmail(ctx, authorEmail); a != nil {
		seen[a.ID] = true
	}
	viewer := damastViewer(ctx)
	out := make([]DamastCoAuthor, 0, len(ids))
	for _, id := range ids {
		u := damastUserByEmail(ctx, id.Email)
		if u != nil {
			if seen[u.ID] {
				continue
			}
			seen[u.ID] = true
			if user_model.IsUserVisibleToViewer(ctx, u, viewer) {
				id.Name = u.GetDisplayName() // the account's name, not whatever the trailer claims
			} else {
				u = nil // hidden account: render like an unknown email
			}
		}
		out = append(out, DamastCoAuthor{Identity: id, User: u})
	}
	return out
}

// damastViewer is the signed-in user of a web request, nil for anonymous viewers and non-web renders.
func damastViewer(ctx context.Context) *user_model.User {
	if tc, ok := ctx.(*Context); ok {
		u, _ := tc.Data["SignedUser"].(*user_model.User)
		return u
	}
	return nil
}

// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package templates

import (
	"context"

	damast_model "forgejo.org/models/damast"
	issues_model "forgejo.org/models/issues"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/damast/suggestion"
	"forgejo.org/modules/log"
)

// DamastSuggestionView is a review suggestion prepared for rendering.
type DamastSuggestionView struct {
	*damast_model.Suggestion
	CanApply  bool   // doer may commit it
	ApplyLink string // web route that commits suggestions
	Message   string // default commit message
}

// DamastSuggestion returns the suggestion of a review comment for rendering, or nil.
// shortcut: the permission check runs per suggestion comment (no request cache); measured at <1 ms per
// comment (100 suggestions: a few ms of a ~1 s page, which is mostly chroma guessing the "suggestion" code block's language).
func DamastSuggestion(ctx context.Context, doer *user_model.User, c *issues_model.Comment) *DamastSuggestionView {
	s, err := damast_model.LoadSuggestion(ctx, c)
	if err != nil {
		log.Error("DamastSuggestion: comment %d: %v", c.ID, err)
		return nil
	}
	if s == nil {
		return nil
	}
	v := &DamastSuggestionView{Suggestion: s}
	if s.Problem != "" || c.Review == nil || c.Review.Type == issues_model.ReviewTypePending {
		return v
	}
	if err := c.LoadIssue(ctx); err != nil {
		log.Error("DamastSuggestion: LoadIssue: %v", err)
		return v
	}
	if err := c.Issue.LoadPullRequest(ctx); err != nil {
		log.Error("DamastSuggestion: LoadPullRequest: %v", err)
		return v
	}
	v.CanApply, err = damast_model.CanApplySuggestions(ctx, doer, nil, c.Issue.PullRequest)
	if err != nil {
		log.Error("DamastSuggestion: CanApplySuggestions: %v", err)
	}
	v.ApplyLink = c.Issue.Link() + "/damast/suggestions/apply"
	v.Message = suggestion.DefaultMessage
	return v
}
